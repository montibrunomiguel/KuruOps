package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/service"
)

const maxBodyBytes = 1 << 20 // 1 MiB; a webhook payload has no business being larger

type Handler struct {
	pool        *db.Pool
	webhooks    *repository.WebhookRepository
	alerts      *service.AlertService
	tags        *service.TagService
	generic     Normalizer
	normalizers map[string]Normalizer
	logger      *slog.Logger
}

func NewHandler(pool *db.Pool, webhooks *repository.WebhookRepository, alerts *service.AlertService, tags *service.TagService, logger *slog.Logger) *Handler {
	return &Handler{
		pool:     pool,
		webhooks: webhooks,
		alerts:   alerts,
		tags:     tags,
		generic:  NewGenericNormalizer(),
		// Keyed by webhook_endpoints.source, lower-cased -- an admin types
		// this in freely when creating an endpoint (see Settings -> Webhook
		// Endpoints), so matching is case-insensitive rather than requiring
		// an exact "wazuh" vs "Wazuh" match. Any source without a dedicated
		// entry here falls back to genericNormalizer.
		normalizers: map[string]Normalizer{
			"wazuh":       NewWazuhNormalizer(),
			"crowdstrike": NewCrowdStrikeNormalizer(),
			"guardduty":   NewGuardDutyNormalizer(),
		},
		logger: logger,
	}
}

// normalizerFor picks the per-source adapter for source, falling back to
// the generic flat-envelope normalizer for anything unrecognized or empty.
func (h *Handler) normalizerFor(source string) Normalizer {
	if n, ok := h.normalizers[strings.ToLower(source)]; ok {
		return n
	}
	return h.generic
}

// ServeHTTP handles POST /hooks with the bearer token in the
// X-Webhook-Token header — the same masked/regenerable token shown in
// Settings -> Webhook Endpoints. Kept as one endpoint rather than
// /hooks/{source} because the source is resolved from the token, not the
// URL, so a token can't be pointed at the wrong endpoint's ingest path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.Header.Get("X-Webhook-Token")
	if token == "" {
		http.Error(w, "missing X-Webhook-Token", http.StatusUnauthorized)
		return
	}
	tokenHash := hashToken(token)

	endpoint, err := h.webhooks.ResolveToken(r.Context(), h.pool, tokenHash)
	if err != nil {
		h.logger.Error("resolve webhook token failed", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if endpoint == nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if endpoint.Status != "active" {
		http.Error(w, "endpoint disabled", http.StatusForbidden)
		return
	}
	if endpoint.ExpiresAt != nil && time.Now().After(*endpoint.ExpiresAt) {
		http.Error(w, "token expired -- regenerate it in Settings > Webhook Endpoints", http.StatusUnauthorized)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	normalized, err := h.normalizerFor(endpoint.Source).Normalize(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var srcIP net.IP
	if normalized.SrcIP != nil {
		srcIP = net.ParseIP(*normalized.SrcIP)
	}

	// A tag the source sent that isn't registered in Settings -> Tags is
	// dropped, not rejected -- an unrecognized tag on one field shouldn't
	// fail ingestion of the whole alert (see domain.Tag / TagService).
	var tags []string
	if len(normalized.Tags) > 0 {
		tags, err = h.tags.FilterKnown(r.Context(), endpoint.TenantID, normalized.Tags)
		if err != nil {
			h.logger.Error("filter tags failed", "error", err, "tenant_id", endpoint.TenantID)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	alert, err := h.alerts.Ingest(r.Context(), endpoint.TenantID, endpoint.ID, domain.Alert{
		ExternalID: normalized.ExternalID,
		Title:      normalized.Title,
		Source:     endpoint.Source,
		Severity:   normalized.Severity,
		RuleID:     normalized.RuleID,
		Asset:      normalized.Asset,
		SrcIP:      srcIP,
		Tags:       tags,
		Payload:    body,
	})
	if err != nil {
		h.logger.Error("ingest alert failed", "error", err, "tenant_id", endpoint.TenantID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"id":"` + alert.ID.String() + `"}`))
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
