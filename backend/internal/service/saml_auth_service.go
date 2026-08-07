package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/authn"
	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// samlMetadataTTL bounds how long a cached IdP *saml.EntityDescriptor is
// trusted before ResolveIDPMetadata is called again -- balances staleness
// (an IdP rotating its signing cert) against the network-fetch-plus-XML-
// parse cost ServeLogin and ServeACS would otherwise both pay on every
// single login (see authn.ResolveIDPMetadata's doc comment).
const samlMetadataTTL = time.Hour

type cachedSAMLMetadata struct {
	entityDescriptor *saml.EntityDescriptor
	fetchedAt        time.Time
}

// SAMLAuthService is the SAML half of "local + LDAP + SAML". It owns the
// three HTTP-facing steps of SP-initiated SSO (metadata, login redirect,
// ACS) and hands a verified assertion to AuthService for the same
// just-in-time provisioning + JWT issuance LDAP uses.
type SAMLAuthService struct {
	pool        *db.Pool
	identityCfg *repository.IdentityConfigRepository
	secrets     secrets.Store
	auth        *AuthService

	metadataMu    sync.Mutex
	metadataCache map[uuid.UUID]cachedSAMLMetadata
}

func NewSAMLAuthService(pool *db.Pool, identityCfg *repository.IdentityConfigRepository, store secrets.Store, auth *AuthService) *SAMLAuthService {
	return &SAMLAuthService{
		pool: pool, identityCfg: identityCfg, secrets: store, auth: auth,
		metadataCache: make(map[uuid.UUID]cachedSAMLMetadata),
	}
}

// InvalidateMetadataCache drops a tenant's cached IdP metadata so the next
// login re-resolves it immediately, instead of waiting out samlMetadataTTL
// -- wired to fire when IdentityConfigService.SaveSAMLConfig changes a
// tenant's IdP metadata source (see cmd/api/main.go).
func (s *SAMLAuthService) InvalidateMetadataCache(tenantID uuid.UUID) {
	s.metadataMu.Lock()
	defer s.metadataMu.Unlock()
	delete(s.metadataCache, tenantID)
}

// resolveIDPMetadata returns the tenant's IdP metadata, serving from cache
// when a fetch happened within samlMetadataTTL. Inline XML config (no
// network fetch either way) is still cached, mainly to avoid re-parsing the
// XML on every login, not because it's expensive to skip.
func (s *SAMLAuthService) resolveIDPMetadata(ctx context.Context, tenantID uuid.UUID, cfg *domain.SAMLConfig) (*saml.EntityDescriptor, error) {
	s.metadataMu.Lock()
	cached, ok := s.metadataCache[tenantID]
	s.metadataMu.Unlock()
	if ok && time.Since(cached.fetchedAt) < samlMetadataTTL {
		return cached.entityDescriptor, nil
	}

	metadata, err := authn.ResolveIDPMetadata(ctx, authn.SAMLParams{
		IDPMetadataURL: cfg.IDPMetadataURL, IDPMetadataXML: cfg.IDPMetadataXML,
	})
	if err != nil {
		return nil, err
	}

	s.metadataMu.Lock()
	s.metadataCache[tenantID] = cachedSAMLMetadata{entityDescriptor: metadata, fetchedAt: time.Now()}
	s.metadataMu.Unlock()
	return metadata, nil
}

func (s *SAMLAuthService) loadConfig(ctx context.Context, tenantID uuid.UUID) (*domain.SAMLConfig, error) {
	var cfg *domain.SAMLConfig
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.identityCfg.GetSAMLConfig(ctx, tx)
		cfg = c
		return err
	})
	return cfg, err
}

// buildServiceProvider resolves the tenant's stored SP keypair + IdP
// metadata into a live *saml.ServiceProvider. The keypair is re-resolved
// (via secrets.Store) on every call -- cheap, no network I/O for the
// EnvStore/Vault/KMS backends this app uses. IdP metadata goes through
// resolveIDPMetadata's cache instead of authn.BuildServiceProvider's
// uncached path, since ServeLogin and ServeACS would otherwise each pay a
// full network-fetch-plus-XML-parse on every single login.
func (s *SAMLAuthService) buildServiceProvider(ctx context.Context, tenantID uuid.UUID, cfg *domain.SAMLConfig) (*saml.ServiceProvider, error) {
	certPEM, err := s.secrets.Resolve(ctx, cfg.SPCertSecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve sp cert: %w", err)
	}
	keyPEM, err := s.secrets.Resolve(ctx, cfg.SPKeySecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve sp key: %w", err)
	}

	idpMetadata, err := s.resolveIDPMetadata(ctx, tenantID, cfg)
	if err != nil {
		return nil, err
	}

	return authn.BuildServiceProviderFromMetadata(authn.SAMLParams{
		EntityID: cfg.SPEntityID, ACSURL: cfg.ACSURL,
		CertPEM: certPEM, KeyPEM: keyPEM,
	}, idpMetadata)
}

func (s *SAMLAuthService) ServeMetadata(ctx context.Context, tenantID uuid.UUID, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig(ctx, tenantID)
	if err != nil || cfg == nil {
		http.Error(w, "saml is not configured for this tenant", http.StatusNotFound)
		return
	}
	sp, err := s.buildServiceProvider(ctx, tenantID, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	authn.ServeMetadata(sp, w)
}

func (s *SAMLAuthService) ServeLogin(ctx context.Context, tenantID uuid.UUID, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig(ctx, tenantID)
	if err != nil || cfg == nil {
		http.Error(w, "saml is not configured for this tenant", http.StatusNotFound)
		return
	}
	sp, err := s.buildServiceProvider(ctx, tenantID, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := authn.RedirectToIDP(sp, w, r, tenantID.String()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ServeACS handles the IdP's POSTed assertion, provisions/updates the user,
// and returns the session token as JSON. Production note: since the
// browser is mid-redirect from the IdP here, a real deployment should hand
// the token to the SPA via a same-origin postMessage or a short-lived
// one-time code exchanged over POST -- never as a URL query parameter,
// which proxies and browser history would log. Returning JSON directly
// (as done here) is a reasonable placeholder for that handoff, not the
// final production mechanism.
func (s *SAMLAuthService) ServeACS(ctx context.Context, tenantID uuid.UUID, w http.ResponseWriter, r *http.Request) {
	cfg, err := s.loadConfig(ctx, tenantID)
	if err != nil || cfg == nil {
		http.Error(w, "saml is not configured for this tenant", http.StatusNotFound)
		return
	}
	sp, err := s.buildServiceProvider(ctx, tenantID, cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	groupAttr := ""
	if cfg.GroupAttribute != nil {
		groupAttr = *cfg.GroupAttribute
	}

	identity, err := authn.ParseAssertion(sp, w, r, groupAttr)
	if err != nil {
		http.Error(w, "invalid saml assertion", http.StatusUnauthorized)
		return
	}

	user, token, refreshToken, err := s.auth.ProvisionFederated(ctx, tenantID, domain.AuthProviderSAML, identity.NameID, identity.NameID, identity.Name, identity.Groups)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":        token,
		"refreshToken": refreshToken,
		"user":         map[string]string{"id": user.ID.String(), "email": user.Email, "name": user.Name, "role": string(user.Role)},
	})
}
