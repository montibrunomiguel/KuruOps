package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/mcpclient"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// MCPToolService is the boundary between the registered mcp_servers config
// and internal/mcpclient's actual JSON-RPC calls. It never invokes a tool
// without first consulting EvaluateToolInvocation -- allow-list and
// side-effecting-tools-require-approval are enforced here, not left to
// whatever code calls ProposeToolCall/ExecuteToolCall.
type MCPToolService struct {
	pool      *db.Pool
	servers   *repository.MCPServerRepository
	toolCalls *repository.AIToolCallRepository
	secrets   secrets.Store

	// onToolCallResolved fires after ApproveToolCall/RejectToolCall settles
	// a call, in case an AIAnalysisService agentic run is paused waiting on
	// it -- wired to AIAnalysisService.ResumeAnalysisRun (see
	// cmd/api/main.go) via a setter, not a constructor param, so the two
	// services don't end up importing each other (AIAnalysisService already
	// depends on *MCPToolService for ProposeToolCall).
	onToolCallResolved func(ctx context.Context, tenantID uuid.UUID, callID int64)
}

func NewMCPToolService(pool *db.Pool, servers *repository.MCPServerRepository, toolCalls *repository.AIToolCallRepository, store secrets.Store) *MCPToolService {
	return &MCPToolService{pool: pool, servers: servers, toolCalls: toolCalls, secrets: store}
}

// SetOnToolCallResolved registers the resume-a-paused-analysis-run hook --
// see the field's doc comment.
func (s *MCPToolService) SetOnToolCallResolved(fn func(ctx context.Context, tenantID uuid.UUID, callID int64)) {
	s.onToolCallResolved = fn
}

// dial resolves a registered server's config + secret into a connected,
// initialized mcpclient.Client. Only transport="http" is dialable today —
// see internal/mcpclient's package doc for why stdio/sse aren't wired up.
func (s *MCPToolService) dial(ctx context.Context, server *domain.MCPServer) (*mcpclient.Client, error) {
	if server.Transport != "http" {
		return nil, fmt.Errorf("transport %q is not implemented yet (only http) -- see internal/mcpclient", server.Transport)
	}

	authToken := ""
	if server.AuthSecretRef != nil {
		t, err := s.secrets.Resolve(ctx, *server.AuthSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve mcp auth token: %w", err)
		}
		authToken = t
	}

	client := mcpclient.New(server.EndpointOrCommand, authToken)
	if err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("connect to mcp server %q: %w", server.Name, err)
	}
	return client, nil
}

// DiscoverTools connects to a registered server and returns everything it
// exposes via tools/list -- the admin picks which of these go into
// AllowedTools/SideEffectingTools from Settings -> MCP Servers. This is a
// live network call, not a cached catalog.
func (s *MCPToolService) DiscoverTools(ctx context.Context, tenantID, serverID uuid.UUID) ([]mcpclient.Tool, error) {
	var server *domain.MCPServer
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.servers.Get(ctx, tx, serverID)
		server = v
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("load mcp server: %w", err)
	}
	if server == nil {
		return nil, fmt.Errorf("mcp server %s not found", serverID)
	}

	client, err := s.dial(ctx, server)
	if err != nil {
		return nil, err
	}
	return client.ListTools(ctx)
}

// ProposeToolCall is what the (not yet built) AI analysis agent calls when
// it wants to use a tool during "Analyze with AI". Non-side-effecting
// tools execute immediately and the row lands as 'executed' right away;
// side-effecting ones stop at 'proposed' and wait for ApproveToolCall or
// RejectToolCall -- the agent never executes a side-effecting tool on its
// own, see architecture review, "IA sugere vs IA executa".
func (s *MCPToolService) ProposeToolCall(ctx context.Context, tenantID, serverID uuid.UUID, contextType string, contextID uuid.UUID, toolName string, args map[string]any) (*domain.AIToolCall, error) {
	var server *domain.MCPServer
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.servers.Get(ctx, tx, serverID)
		server = v
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("load mcp server: %w", err)
	}
	if server == nil {
		return nil, fmt.Errorf("mcp server %s not found", serverID)
	}

	policy := EvaluateToolInvocation(*server, toolName)
	if !policy.Allowed {
		return nil, fmt.Errorf("tool %q is not in the allow-list for mcp server %q", toolName, server.Name)
	}

	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode tool args: %w", err)
	}

	call := &domain.AIToolCall{
		TenantID:    tenantID,
		MCPServerID: serverID,
		ToolName:    toolName,
		ContextType: contextType,
		ContextID:   contextID,
		Args:        argsJSON,
		Status:      domain.ToolCallProposed,
	}

	err = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.toolCalls.Insert(ctx, tx, call)
	})
	if err != nil {
		return nil, fmt.Errorf("record tool call: %w", err)
	}

	if !policy.RequiresApproval {
		if err := s.execute(ctx, tenantID, call, server, args); err != nil {
			return call, err
		}
	}
	return call, nil
}

// ApproveToolCall records the analyst's approval and executes the call.
func (s *MCPToolService) ApproveToolCall(ctx context.Context, tenantID uuid.UUID, callID int64, approverID uuid.UUID) error {
	var call *domain.AIToolCall
	var server *domain.MCPServer
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.toolCalls.Get(ctx, tx, callID)
		if err != nil {
			return err
		}
		if c == nil {
			return fmt.Errorf("tool call %d not found", callID)
		}
		if c.Status != domain.ToolCallProposed {
			return fmt.Errorf("tool call %d is not awaiting approval (status=%s)", callID, c.Status)
		}
		srv, err := s.servers.Get(ctx, tx, c.MCPServerID)
		if err != nil {
			return err
		}
		if err := s.toolCalls.SetStatus(ctx, tx, callID, domain.ToolCallApproved, &approverID); err != nil {
			return err
		}
		call, server = c, srv
		return nil
	})
	if err != nil {
		return err
	}

	var args map[string]any
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return fmt.Errorf("decode stored tool args: %w", err)
	}
	execErr := s.execute(ctx, tenantID, call, server, args)
	if s.onToolCallResolved != nil {
		s.onToolCallResolved(ctx, tenantID, callID)
	}
	return execErr
}

func (s *MCPToolService) RejectToolCall(ctx context.Context, tenantID uuid.UUID, callID int64, approverID uuid.UUID) error {
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.toolCalls.Get(ctx, tx, callID)
		if err != nil {
			return err
		}
		if c == nil {
			return fmt.Errorf("tool call %d not found", callID)
		}
		if c.Status != domain.ToolCallProposed {
			return fmt.Errorf("tool call %d is not awaiting approval (status=%s)", callID, c.Status)
		}
		return s.toolCalls.SetStatus(ctx, tx, callID, domain.ToolCallRejected, &approverID)
	})
	if err == nil && s.onToolCallResolved != nil {
		s.onToolCallResolved(ctx, tenantID, callID)
	}
	return err
}

// GetToolCall backs the alert/incident-scoped inline approve/reject
// handlers (see AlertHandlers/IncidentHandlers) -- they need the call's own
// ContextType/ContextID to confirm it actually belongs to the alert/
// incident named in the URL before approving/rejecting it, the same
// ownership check every other alert/incident sub-resource endpoint already
// does.
func (s *MCPToolService) GetToolCall(ctx context.Context, tenantID uuid.UUID, callID int64) (*domain.AIToolCall, error) {
	var call *domain.AIToolCall
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.toolCalls.Get(ctx, tx, callID)
		call = c
		return err
	})
	return call, err
}

func (s *MCPToolService) PendingApprovals(ctx context.Context, tenantID uuid.UUID) ([]domain.AIToolCall, error) {
	var calls []domain.AIToolCall
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.toolCalls.ListPending(ctx, tx)
		calls = v
		return err
	})
	return calls, err
}

// execute actually calls the tool over MCP and records the outcome.
// Failure to reach the server lands the call as 'failed', not an error the
// caller has to translate — the row itself is the audit record either way.
// The final Status/Result are mirrored onto call itself (not just written
// to the DB) so a caller holding the struct ProposeToolCall/ApproveToolCall
// returned -- see AIAnalysisService's agentic loop, which needs the actual
// result to feed back to the model -- doesn't have to re-fetch it.
func (s *MCPToolService) execute(ctx context.Context, tenantID uuid.UUID, call *domain.AIToolCall, server *domain.MCPServer, args map[string]any) error {
	client, err := s.dial(ctx, server)
	if err != nil {
		s.recordFailure(ctx, tenantID, call, err)
		return err
	}

	result, err := client.CallTool(ctx, call.ToolName, args)
	if err != nil {
		s.recordFailure(ctx, tenantID, call, err)
		return err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode tool result: %w", err)
	}
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.toolCalls.SetResult(ctx, tx, call.ID, domain.ToolCallExecuted, resultJSON)
	}); err != nil {
		return err
	}
	call.Status, call.Result = domain.ToolCallExecuted, resultJSON
	return nil
}

func (s *MCPToolService) recordFailure(ctx context.Context, tenantID uuid.UUID, call *domain.AIToolCall, cause error) {
	payload, _ := json.Marshal(map[string]string{"error": cause.Error()})
	_ = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.toolCalls.SetResult(ctx, tx, call.ID, domain.ToolCallFailed, payload)
	})
	call.Status, call.Result = domain.ToolCallFailed, payload
}
