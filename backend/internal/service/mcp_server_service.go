package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
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
// admin audit event's data column -- AuthSecretRef is an opaque reference
// into secrets.Store, not the plaintext token, but is left out anyway since
// it's meaningless to a human reader; "authTokenSet" signals whether one is
// configured without exposing it.
func mcpServerAuditFields(s *domain.MCPServer) map[string]any {
	return map[string]any{
		"name": s.Name, "transport": s.Transport, "endpointOrCommand": s.EndpointOrCommand,
		"authTokenSet": s.AuthSecretRef != nil,
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
	AuthToken          string // plaintext, resolved to a secrets.Store ref; "" means no auth / keep existing on update
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

	var authRef *string
	if in.AuthToken != "" {
		ref, err := s.secrets.Put(ctx, tenantID.String(), "mcp:"+in.Name, in.AuthToken)
		if err != nil {
			return nil, fmt.Errorf("store mcp auth token: %w", err)
		}
		authRef = &ref
	}

	server := &domain.MCPServer{
		TenantID:          tenantID,
		Name:              in.Name,
		Transport:         in.Transport,
		EndpointOrCommand: in.EndpointOrCommand,
		AuthSecretRef:     authRef,
		// mcp_servers.allowed_tools/enabled_for/side_effecting_tools are all NOT NULL
		AllowedTools:       orEmptySlice(in.AllowedTools),
		EnabledFor:         orEmptySlice(in.EnabledFor),
		SideEffectingTools: orEmptySlice(in.SideEffectingTools),
		CreatedBy:          &actorID,
	}

	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
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

func (s *MCPServerService) Update(ctx context.Context, tenantID, actorID, id uuid.UUID, in MCPServerSaveInput) (*domain.MCPServer, error) {
	if err := validateToolLists(in.AllowedTools, in.SideEffectingTools); err != nil {
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

		authRef := existing.AuthSecretRef
		if in.AuthToken != "" {
			ref, err := s.secrets.Put(ctx, tenantID.String(), "mcp:"+in.Name, in.AuthToken)
			if err != nil {
				return fmt.Errorf("store mcp auth token: %w", err)
			}
			authRef = &ref
		}

		existing.Name = in.Name
		existing.Transport = in.Transport
		existing.EndpointOrCommand = in.EndpointOrCommand
		existing.AuthSecretRef = authRef
		existing.AllowedTools = orEmptySlice(in.AllowedTools)
		existing.EnabledFor = orEmptySlice(in.EnabledFor)
		existing.SideEffectingTools = orEmptySlice(in.SideEffectingTools)

		if err := s.repo.Update(ctx, tx, existing); err != nil {
			return fmt.Errorf("update mcp server: %w", err)
		}
		updated = existing

		data, _ := json.Marshal(map[string]any{"from": before, "to": mcpServerAuditFields(existing)})
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
