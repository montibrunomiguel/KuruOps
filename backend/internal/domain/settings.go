package domain

import (
	"time"

	"github.com/google/uuid"
)

// WebhookEndpoint mirrors `webhook_endpoints`. The bearer token itself is
// never persisted or returned by the API after creation/regeneration — only
// its hash (TokenHash, used to authenticate cmd/ingest requests) and last 4
// characters (TokenLast4, shown in Settings so an admin can recognize which
// token is which without ever seeing the full value again).
type WebhookEndpoint struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenantId"`
	Name       string     `json:"name"`
	Source     string     `json:"source"`
	TokenHash  string     `json:"-"`
	TokenLast4 string     `json:"tokenLast4"`
	Status     string     `json:"status"`
	RotatedAt  *time.Time `json:"rotatedAt,omitempty"`
	// ExpiresAt is nil for an endpoint an admin has explicitly opted out of
	// expiring; Create/Regenerate default it to now()+90d otherwise (see
	// service.WebhookService). cmd/ingest rejects a token past this.
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	CreatedBy *uuid.UUID `json:"createdBy,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	// FieldMappingTemplateID is optional and, unlike Name/Source, changeable
	// after creation (see WebhookHandlers' PUT /{id}/field-mapping-template)
	// -- cmd/ingest applies the referenced FieldMappingTemplate's rules on
	// top of the automatic metadata extraction for every alert this endpoint
	// receives (see internal/ingest/field_mapping.go).
	FieldMappingTemplateID *uuid.UUID `json:"fieldMappingTemplateId,omitempty"`
	// GroupByFields is the JSON-path list (dot notation, same syntax as
	// FieldMappingRule.JSONPath -- see internal/jsonpath) an admin uses to
	// say "these fields together identify the same event". Empty (the
	// default) means dedup is off for this endpoint. Editable after
	// creation via WebhookHandlers' PUT /{id}/group-by-fields, same as
	// FieldMappingTemplateID. See AlertService.computeGroupKey/Ingest for
	// how this drives suppressing a repeat alert instead of creating a new
	// one (domain.Alert.DuplicateCount).
	GroupByFields []string `json:"groupByFields"`
	// DedupWindowMinutes is how long a match against GroupByFields still
	// counts as "the same event" -- a payload with the same field values
	// arriving after this many minutes starts a new alert instead of
	// incrementing an old one. Only meaningful when GroupByFields is
	// non-empty; defaults to 30 (see service.WebhookService's
	// resolveDedupWindow), same "sensible default, explicitly overridable"
	// shape as escalation_policies.unacknowledged_after_minutes.
	DedupWindowMinutes int `json:"dedupWindowMinutes"`
}

// LLMProvider mirrors `llm_providers`. Kind "openai_compatible" with a
// custom BaseURL is the general-purpose case — it covers self-hosted
// runtimes (vLLM, Ollama) and most enterprise-hosted providers without a
// dedicated adapter per vendor (see architecture review, section 4).
// APIKeySecretRef points into the secret manager; the key itself is never
// stored in this table or returned by the API.
type LLMProvider struct {
	ID              uuid.UUID `json:"id"`
	TenantID        uuid.UUID `json:"tenantId"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	BaseURL         *string   `json:"baseUrl,omitempty"`
	Model           string    `json:"model"`
	APIKeySecretRef string    `json:"-"`
	IsDefault       bool      `json:"isDefault"`
	// AutoAnalyzeAllAlerts, when true and this is the tenant's default
	// provider, makes AlertService.Ingest's auto-analysis trigger actually
	// fire for every incoming webhook alert (see AIAnalysisService.
	// buildClient). Default false: an analyst clicking "Analyze with AI" is
	// always available regardless of this flag, this only controls the
	// unattended, fires-on-every-alert path.
	AutoAnalyzeAllAlerts bool       `json:"autoAnalyzeAllAlerts"`
	CreatedBy            *uuid.UUID `json:"createdBy,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// MCPServer mirrors `mcp_servers`. AllowedTools is an explicit allow-list —
// tools the server exposes that aren't listed here are never offered to the
// analysis agent. SideEffectingTools is a stricter sub-list: even when
// allowed, these always require analyst approval before executing (see
// ai_tool_calls.status and the architecture review's "IA sugere vs IA
// executa" principle).
type MCPServer struct {
	ID                uuid.UUID `json:"id"`
	TenantID          uuid.UUID `json:"tenantId"`
	Name              string    `json:"name"`
	Transport         string    `json:"transport"`
	EndpointOrCommand string    `json:"endpointOrCommand"`
	// AuthType selects how requests to the server are authenticated; see the
	// MCPAuth* constants. The non-secret parameters of each type are
	// serialized (the admin UI needs to show which header or client id is
	// configured); every secret is only ever an opaque secrets.Store ref
	// tagged json:"-". A type that needs a secret implies it is set -- the
	// mcp_servers_auth_shape_check constraint guarantees it -- so no separate
	// "secret set" flag is exposed.
	AuthType string `json:"authType"`
	// AuthHeaderName is the custom header an api_key credential is sent in.
	AuthHeaderName *string `json:"authHeaderName,omitempty"`
	// AuthSecretRef holds the api_key value or the bearer token.
	AuthSecretRef *string `json:"-"`
	// OAuthTokenURL/OAuthClientID/OAuthClientSecretRef configure the OAuth
	// 2.0 client_credentials grant used when AuthType is MCPAuthOAuth.
	OAuthTokenURL        *string    `json:"oauthTokenUrl,omitempty"`
	OAuthClientID        *string    `json:"oauthClientId,omitempty"`
	OAuthClientSecretRef *string    `json:"-"`
	AllowedTools         []string   `json:"allowedTools"`
	EnabledFor           []string   `json:"enabledFor"`
	SideEffectingTools   []string   `json:"sideEffectingTools"`
	IsEnabled            bool       `json:"isEnabled"`
	CreatedBy            *uuid.UUID `json:"createdBy,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// MCPServer.AuthType values -- kept in sync with the
// mcp_servers_auth_type_check constraint.
const (
	MCPAuthNone   = "none"
	MCPAuthAPIKey = "api_key"
	MCPAuthBearer = "bearer"
	MCPAuthOAuth  = "oauth"
)
