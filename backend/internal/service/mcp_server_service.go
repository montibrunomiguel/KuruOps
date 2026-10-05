package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/secrets"
)

// mcpServerRepo is the subset of *repository.MCPServerRepository this
// service calls -- an interface, not the concrete type, purely so tests can
// substitute a repo double that fails on demand to exercise the
// error-wrapping branches (a DB call failing mid-transaction) a real
// Postgres integration test can't trigger. *repository.MCPServerRepository
// already satisfies this implicitly, so every existing constructor call
// site is unaffected -- including the OTHER services (MCPToolService,
// AIAnalysisService) that also depend on the concrete repository type
// directly; that's a separate field on a separate struct and is untouched.
type mcpServerRepo interface {
	List(ctx context.Context, tx pgx.Tx) ([]domain.MCPServer, error)
	Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.MCPServer, error)
	Insert(ctx context.Context, tx pgx.Tx, s *domain.MCPServer) error
	Update(ctx context.Context, tx pgx.Tx, s *domain.MCPServer) error
	SetEnabled(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool) error
	Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error
}

type MCPServerService struct {
	pool    *db.Pool
	repo    mcpServerRepo
	secrets secrets.Store
	audit   *repository.AdminAuditEventRepository
}

func NewMCPServerService(pool *db.Pool, repo mcpServerRepo, store secrets.Store, audit *repository.AdminAuditEventRepository) *MCPServerService {
	return &MCPServerService{pool: pool, repo: repo, secrets: store, audit: audit}
}

// mcpServerAuditFields is the subset of domain.MCPServer safe to put in an
// admin audit event's data column. Secret refs are opaque pointers into
// secrets.Store, meaningless to a human reader, so they are left out; the auth
// type (which implies a credential is set -- see mcp_servers_auth_shape_check)
// and its non-secret parameters are enough to answer "what changed".
func mcpServerAuditFields(s *domain.MCPServer) map[string]any {
	return map[string]any{
		"name": s.Name, "transport": s.Transport, "endpointOrCommand": s.EndpointOrCommand,
		"authType": s.AuthType, "authHeaderName": s.AuthHeaderName,
		"oauthTokenUrl": s.OAuthTokenURL, "oauthClientId": s.OAuthClientID,
		"allowedTools": s.AllowedTools, "enabledFor": s.EnabledFor, "sideEffectingTools": s.SideEffectingTools,
		"isEnabled": s.IsEnabled,
	}
}

func (s *MCPServerService) List(ctx context.Context, tenantID uuid.UUID) ([]domain.MCPServer, error) {
	var servers []domain.MCPServer
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.repo.List(ctx, tx)
		servers = v
		return err
	})
	return servers, err
}

type MCPServerSaveInput struct {
	Name               string
	Transport          string
	EndpointOrCommand  string
	Auth               MCPServerAuthInput
	AllowedTools       []string
	EnabledFor         []string
	SideEffectingTools []string
}

// Create registers an MCP server. AllowedTools is the explicit allow-list
// discovered from the server's tools/list and approved by the admin — tools
// it exposes outside this list are never offered to the analysis agent.
// SideEffectingTools must be a subset of AllowedTools: a tool has to be
// allowed at all before "requires human approval" is a meaningful flag on
// it (see architecture review, "IA sugere vs IA executa").
func (s *MCPServerService) Create(ctx context.Context, tenantID, actorID uuid.UUID, in MCPServerSaveInput) (*domain.MCPServer, error) {
	if err := validateToolLists(in.AllowedTools, in.SideEffectingTools); err != nil {
		return nil, err
	}
	if err := validateEndpoint(in.EndpointOrCommand); err != nil {
		return nil, err
	}

	server := &domain.MCPServer{
		TenantID:          tenantID,
		Name:              in.Name,
		Transport:         in.Transport,
		EndpointOrCommand: in.EndpointOrCommand,
		// mcp_servers.allowed_tools/enabled_for/side_effecting_tools are all NOT NULL
		AllowedTools:       orEmptySlice(in.AllowedTools),
		EnabledFor:         orEmptySlice(in.EnabledFor),
		SideEffectingTools: orEmptySlice(in.SideEffectingTools),
		CreatedBy:          &actorID,
	}

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// Checked before any secret is stored: PersistentEnvStore and Vault
		// key a secret by (tenant, purpose) alone, so storing one for a name
		// that already exists would overwrite that server's credential --
		// before the unique constraint got the chance to reject the insert.
		if err := s.ensureNameFree(ctx, tx, in.Name, uuid.Nil); err != nil {
			return err
		}
		if _, err := s.applyAuth(ctx, tenantID, server, nil, in.Auth); err != nil {
			return err
		}
		if err := s.repo.Insert(ctx, tx, server); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"from": nil, "to": mcpServerAuditFields(server)})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "mcp-servers", Action: "create", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("create mcp server: %w", err)
	}
	return server, nil
}

// ensureNameFree rejects a name another server already uses. exceptID is the
// server being renamed (uuid.Nil on create).
func (s *MCPServerService) ensureNameFree(ctx context.Context, tx pgx.Tx, name string, exceptID uuid.UUID) error {
	all, err := s.repo.List(ctx, tx)
	if err != nil {
		return fmt.Errorf("check mcp server name: %w", err)
	}
	for _, other := range all {
		if other.Name == name && other.ID != exceptID {
			return fmt.Errorf("an MCP server named %q already exists", name)
		}
	}
	return nil
}

func (s *MCPServerService) Update(ctx context.Context, tenantID, actorID, id uuid.UUID, in MCPServerSaveInput) (*domain.MCPServer, error) {
	if err := validateToolLists(in.AllowedTools, in.SideEffectingTools); err != nil {
		return nil, err
	}
	if err := validateEndpoint(in.EndpointOrCommand); err != nil {
		return nil, err
	}

	var updated *domain.MCPServer
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		existing, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("load mcp server: %w", err)
		}
		if existing == nil {
			return fmt.Errorf("mcp server %s not found", id)
		}
		before := mcpServerAuditFields(existing)
		prev := *existing

		if in.Name != existing.Name {
			if err := s.ensureNameFree(ctx, tx, in.Name, id); err != nil {
				return err
			}
		}

		existing.Name = in.Name
		existing.Transport = in.Transport
		existing.EndpointOrCommand = in.EndpointOrCommand
		existing.AllowedTools = orEmptySlice(in.AllowedTools)
		existing.EnabledFor = orEmptySlice(in.EnabledFor)
		existing.SideEffectingTools = orEmptySlice(in.SideEffectingTools)

		rotated, err := s.applyAuth(ctx, tenantID, existing, &prev, in.Auth)
		if err != nil {
			return err
		}

		if err := s.repo.Update(ctx, tx, existing); err != nil {
			return fmt.Errorf("update mcp server: %w", err)
		}
		updated = existing

		after := mcpServerAuditFields(existing)
		// A same-type rotation leaves every audited field unchanged, which
		// would make the event look like a no-op.
		after["credentialRotated"] = rotated
		data, _ := json.Marshal(map[string]any{"from": before, "to": after})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "mcp-servers", Action: "update", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *MCPServerService) SetEnabled(ctx context.Context, tenantID, actorID, id uuid.UUID, enabled bool) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if err := s.repo.SetEnabled(ctx, tx, id, enabled); err != nil {
			return err
		}
		data, _ := json.Marshal(map[string]any{"to": map[string]any{"isEnabled": enabled}})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "mcp-servers", Action: "set-enabled", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

func (s *MCPServerService) Delete(ctx context.Context, tenantID, actorID, id uuid.UUID) error {
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		before, err := s.repo.Get(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := s.repo.Delete(ctx, tx, id); err != nil {
			return err
		}
		var from any
		if before != nil {
			from = mcpServerAuditFields(before)
		}
		data, _ := json.Marshal(map[string]any{"from": from, "to": nil})
		return s.audit.InsertEvent(ctx, tx, &domain.AdminAuditEvent{
			TenantID: tenantID, Area: "mcp-servers", Action: "delete", ActorType: domain.ActorUser, ActorID: actorID, Data: data,
		})
	})
}

// validateEndpoint rejects an endpoint that isn't a dialable http(s) URL.
//
// Only the "Streamable HTTP" transport is actually implemented (see package
// mcpclient), so anything else -- a bare hostname, a stdio command, a
// file:// path -- was accepted at save time and only surfaced much later as
// a 502 from Discover Tools, with the transport error as the only clue.
// Catching it here means the admin is told while the form is still open.
//
// This is not the SSRF control: httpguard still refuses to dial private
// addresses at request time, and must, since DNS can resolve a perfectly
// public-looking name to 127.0.0.1 long after this check has passed.
func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint %q is not a valid URL", endpoint)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint must be an http:// or https:// URL, got %q", endpoint)
	}
	if u.Host == "" {
		return fmt.Errorf("endpoint %q is missing a host", endpoint)
	}
	// A user:password@ in the URL would be stored in plaintext in the
	// database and echoed into the audit log, sidestepping secrets.Store --
	// there are proper authentication types for this now.
	if u.User != nil {
		return errors.New("endpoint must not contain embedded credentials; use an authentication type instead")
	}
	return nil
}

func validateToolLists(allowed, sideEffecting []string) error {
	for _, tool := range sideEffecting {
		if !slices.Contains(allowed, tool) {
			return fmt.Errorf("side-effecting tool %q must also be in allowed_tools", tool)
		}
	}
	return nil
}

// ToolInvocationPolicy is what AIAnalysisService.ProposeToolCall (via
// MCPToolService) consults before calling any MCP tool: allowed at all, and
// if so, whether it needs analyst approval first via the ai_tool_calls
// proposed/approved flow. Runs inline in cmd/api/cmd/ingest's agentic
// analysis loop, not in cmd/worker -- see runAgentAnalysis in
// ai_analysis_service.go.
type ToolInvocationPolicy struct {
	Allowed          bool
	RequiresApproval bool
}

func EvaluateToolInvocation(server domain.MCPServer, toolName string) ToolInvocationPolicy {
	if !server.IsEnabled || !slices.Contains(server.AllowedTools, toolName) {
		return ToolInvocationPolicy{Allowed: false}
	}
	return ToolInvocationPolicy{
		Allowed:          true,
		RequiresApproval: slices.Contains(server.SideEffectingTools, toolName),
	}
}
