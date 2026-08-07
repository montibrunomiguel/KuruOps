package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/db"
	"github.com/argusops/argusops/internal/domain"
	"github.com/argusops/argusops/internal/llmclient"
	"github.com/argusops/argusops/internal/repository"
	"github.com/argusops/argusops/internal/secrets"
)

// AIAnalysisService runs an "Analyze with AI" request against the tenant's
// default LLM provider (see Settings -> AI Integration) and logs the result
// as an alert_events/incident_events row so it shows up in the timeline
// alongside every other change. It's synchronous -- the frontend shows a
// loading state while the request (which can take several seconds, longer
// if it uses MCP tools) is in flight, rather than polling a background job.
//
// When the tenant has an MCP server enabled for this analysis type (see
// domain.MCPServer.EnabledFor), analysis runs as an agentic tool-use loop
// instead of a single LLM call -- see runAgentAnalysis. A tenant with no
// MCP servers configured (the common case) gets the exact same single-call
// behavior this had before the loop existed.
type AIAnalysisService struct {
	pool         *db.Pool
	llmProviders *repository.LLMProviderRepository
	alerts       *repository.AlertRepository
	incidents    *repository.IncidentRepository
	secrets      secrets.Store
	mcpServers   *repository.MCPServerRepository
	mcpTools     *MCPToolService
	runs         *repository.AIAnalysisRunRepository
	toolCalls    *repository.AIToolCallRepository
}

func NewAIAnalysisService(
	pool *db.Pool,
	llmProviders *repository.LLMProviderRepository,
	alerts *repository.AlertRepository,
	incidents *repository.IncidentRepository,
	store secrets.Store,
	mcpServers *repository.MCPServerRepository,
	mcpTools *MCPToolService,
	runs *repository.AIAnalysisRunRepository,
	toolCalls *repository.AIToolCallRepository,
) *AIAnalysisService {
	return &AIAnalysisService{
		pool: pool, llmProviders: llmProviders, alerts: alerts, incidents: incidents, secrets: store,
		mcpServers: mcpServers, mcpTools: mcpTools, runs: runs, toolCalls: toolCalls,
	}
}

const analysisSystemPrompt = `You are a SOC (Security Operations Center) analyst assistant. Given the details of a security alert or incident, provide a concise triage analysis: likely nature of the activity, whether it appears to be a true or false positive, and recommended next steps. Keep the response focused and actionable, a few short paragraphs at most. You may have tools available to look up additional context (e.g. threat intel, asset info) before answering -- use them when they would materially improve your analysis, but you don't have to use every tool offered.`

// maxAgenticTurns bounds an agentic run's total LLM round-trips, so a model
// that keeps calling tools without ever settling on an answer can't run up
// unbounded cost/latency (or, worse, loop forever across resumes).
const maxAgenticTurns = 5

// pausedForApprovalMessage is what AnalyzeAlert/AnalyzeIncident return (as
// a normal, non-error result -- see analyzeResponse{Result: ...} in the
// alerts/incidents handlers) when a side-effecting tool call pauses the
// run. The final analysis text isn't available yet; it lands as a fresh
// ai_analysis_run event once ResumeAnalysisRun completes the run after
// approval.
const pausedForApprovalMessage = "Analysis paused: a tool call requires analyst approval before continuing (Settings -> MCP Servers -> Pending Approvals). The final result will be logged to this %s's timeline once it's approved and the analysis resumes."

// AnalyzeAlert loads alert (subject to allowedTags, same visibility rule as
// every other AlertService method), then either sends its details straight
// to the tenant's default LLM provider (no MCP servers enabled for
// "alert_analysis") or drives an agentic tool-use loop (see
// runAgentAnalysis). Either way the final result is recorded as an
// ai_analysis_run event once available.
func (s *AIAnalysisService) AnalyzeAlert(ctx context.Context, tenantID, alertID, actorID uuid.UUID, allowedTags []string) (string, error) {
	var alert *domain.Alert
	var client llmclient.Client
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		a, err := s.alerts.Get(ctx, tx, alertID)
		if err != nil {
			return fmt.Errorf("load alert: %w", err)
		}
		if a == nil || !tagsVisible(allowedTags, a.Tags) {
			return fmt.Errorf("alert %s not found", alertID)
		}
		alert = a

		c, err := s.buildClient(ctx, tx)
		if err != nil {
			return err
		}
		client = c
		return nil
	})
	if err != nil {
		return "", err
	}

	tools, routes, err := s.resolveAgentTools(ctx, tenantID, "alert_analysis")
	if err != nil {
		return "", err
	}

	if len(tools) == 0 {
		return s.analyzeSimple(ctx, tenantID, actorID, "alert", alertID, client, alertPrompt(alert))
	}
	return s.runAgentAnalysis(ctx, tenantID, actorID, "alert", alertID, client, tools, routes, alertPrompt(alert))
}

// AnalyzeIncident is AnalyzeAlert's counterpart for incidents. Incidents
// have no tag-visibility guard on Get elsewhere in this codebase (see
// IncidentService.Get), so this doesn't apply one either.
func (s *AIAnalysisService) AnalyzeIncident(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, allowedTags []string) (string, error) {
	var incident *domain.Incident
	var client llmclient.Client
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		inc, err := s.incidents.Get(ctx, tx, incidentID)
		if err != nil {
			return fmt.Errorf("load incident: %w", err)
		}
		if inc == nil || !tagsVisible(allowedTags, inc.Tags) {
			return fmt.Errorf("incident %s not found", incidentID)
		}
		incident = inc

		c, err := s.buildClient(ctx, tx)
		if err != nil {
			return err
		}
		client = c
		return nil
	})
	if err != nil {
		return "", err
	}

	tools, routes, err := s.resolveAgentTools(ctx, tenantID, "incident_analysis")
	if err != nil {
		return "", err
	}

	if len(tools) == 0 {
		return s.analyzeSimple(ctx, tenantID, actorID, "incident", incidentID, client, incidentPrompt(incident))
	}
	return s.runAgentAnalysis(ctx, tenantID, actorID, "incident", incidentID, client, tools, routes, incidentPrompt(incident))
}

// analyzeSimple is the original, pre-agentic-loop behavior: one LLM call,
// no tools. Kept as its own path (rather than routing everything through
// the agentic loop with zero tools) so the common case -- no MCP servers
// configured -- has exactly the same shape it always did.
func (s *AIAnalysisService) analyzeSimple(ctx context.Context, tenantID, actorID uuid.UUID, contextType string, contextID uuid.UUID, client llmclient.Client, userPrompt string) (string, error) {
	text, err := client.Complete(ctx, analysisSystemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("llm analysis: %w", err)
	}
	if err := s.recordEvent(ctx, tenantID, actorID, contextType, contextID, text); err != nil {
		return "", err
	}
	return text, nil
}

// agentToolRoute is resolveAgentTools' answer to "if the model calls tool
// X, which MCP server serves it, and does it need analyst approval first" --
// resolved once per analysis (not re-evaluated turn to turn), and persisted
// verbatim on the ai_analysis_runs row so a resume doesn't have to
// re-discover tools or re-evaluate policy (which could have changed under
// an in-progress run if an admin edited the server's config mid-analysis).
type agentToolRoute struct {
	ServerID         uuid.UUID `json:"serverId"`
	RequiresApproval bool      `json:"requiresApproval"`
}

// resolveAgentTools finds every MCP server enabled for analysisType
// ("alert_analysis" | "incident_analysis"), discovers its tools, and keeps
// only the ones on that server's admin-configured allow-list -- the model
// is never even offered a tool it isn't allowed to call. A server that
// fails to respond to discovery is skipped, not fatal to the whole
// analysis (best-effort, same principle as ingest's unknown-tag handling).
// On a tool-name collision across servers, the first server (list order)
// wins.
func (s *AIAnalysisService) resolveAgentTools(ctx context.Context, tenantID uuid.UUID, analysisType string) ([]llmclient.Tool, map[string]agentToolRoute, error) {
	var servers []domain.MCPServer
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		v, err := s.mcpServers.List(ctx, tx)
		servers = v
		return err
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list mcp servers: %w", err)
	}

	var tools []llmclient.Tool
	routes := map[string]agentToolRoute{}
	for _, server := range servers {
		if !server.IsEnabled || !slices.Contains(server.EnabledFor, analysisType) {
			continue
		}
		discovered, err := s.mcpTools.DiscoverTools(ctx, tenantID, server.ID)
		if err != nil {
			continue
		}
		for _, dt := range discovered {
			if !slices.Contains(server.AllowedTools, dt.Name) {
				continue
			}
			if _, exists := routes[dt.Name]; exists {
				continue
			}
			tools = append(tools, llmclient.Tool{Name: dt.Name, Description: dt.Description, InputSchema: dt.InputSchema})
			routes[dt.Name] = agentToolRoute{
				ServerID:         server.ID,
				RequiresApproval: slices.Contains(server.SideEffectingTools, dt.Name),
			}
		}
	}
	return tools, routes, nil
}

// runAgentAnalysis starts a fresh agentic run: persists an ai_analysis_runs
// row up front (so even a first-turn pause has something ResumeAnalysisRun
// can find), then drives turns via driveAgentLoop.
func (s *AIAnalysisService) runAgentAnalysis(ctx context.Context, tenantID, actorID uuid.UUID, contextType string, contextID uuid.UUID, client llmclient.Client, tools []llmclient.Tool, routes map[string]agentToolRoute, userPrompt string) (string, error) {
	messages := []llmclient.Message{{Role: llmclient.RoleUser, Content: userPrompt}}

	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return "", fmt.Errorf("encode initial messages: %w", err)
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return "", fmt.Errorf("encode tools: %w", err)
	}
	routesJSON, err := json.Marshal(routes)
	if err != nil {
		return "", fmt.Errorf("encode tool routes: %w", err)
	}

	run := &domain.AIAnalysisRun{
		TenantID: tenantID, ContextType: contextType, ContextID: contextID, ActorID: actorID,
		Status: domain.AIAnalysisRunRunning, Messages: messagesJSON, Tools: toolsJSON, ToolRoutes: routesJSON,
	}
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.Insert(ctx, tx, run)
	}); err != nil {
		return "", fmt.Errorf("create analysis run: %w", err)
	}

	return s.driveAgentLoop(ctx, run, client, tools, routes, messages, 0)
}

// ResumeAnalysisRun picks a paused run back up after its pending tool call
// was approved (and executed) or rejected -- see
// MCPToolService.onToolCallResolved. It's a no-op (not an error) when
// callID isn't what any paused run was waiting on, since ApproveToolCall/
// RejectToolCall fire this for every tool call, most of which have nothing
// to do with an agentic analysis run at all.
//
// Errors here are logged onto the run itself (status='failed') rather than
// returned to the caller -- this runs from inside
// MCPToolService.ApproveToolCall/RejectToolCall, which already has its own
// success/failure to report about the tool call itself; a downstream
// resume failure shouldn't turn that into an error too.
func (s *AIAnalysisService) ResumeAnalysisRun(ctx context.Context, tenantID uuid.UUID, callID int64) {
	var run *domain.AIAnalysisRun
	var toolCall *domain.AIToolCall
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		r, err := s.runs.GetPausedByToolCall(ctx, tx, callID)
		if err != nil {
			return err
		}
		run = r
		if run == nil {
			return nil
		}
		tc, err := s.toolCalls.Get(ctx, tx, callID)
		toolCall = tc
		return err
	})
	if err != nil || run == nil {
		return
	}

	var messages []llmclient.Message
	if err := json.Unmarshal(run.Messages, &messages); err != nil {
		s.failRun(ctx, tenantID, run.ID, fmt.Errorf("decode saved conversation: %w", err))
		return
	}
	var tools []llmclient.Tool
	if err := json.Unmarshal(run.Tools, &tools); err != nil {
		s.failRun(ctx, tenantID, run.ID, fmt.Errorf("decode saved tools: %w", err))
		return
	}
	var routes map[string]agentToolRoute
	if err := json.Unmarshal(run.ToolRoutes, &routes); err != nil {
		s.failRun(ctx, tenantID, run.ID, fmt.Errorf("decode saved tool routes: %w", err))
		return
	}

	// The provider's own tool_call ID (needed to correlate the result with
	// the specific tool_use block the model emitted) isn't stored on
	// domain.AIToolCall -- it's already sitting in the last persisted
	// message, though: driveAgentLoop appends the assistant's tool-call
	// message to `messages` before pausing, so its last entry is exactly
	// the turn that proposed toolCall.
	toolCallID := ""
	if len(messages) > 0 {
		last := messages[len(messages)-1]
		if last.Role == llmclient.RoleAssistant && len(last.ToolCalls) > 0 {
			toolCallID = last.ToolCalls[0].ID
		}
	}
	messages = append(messages, toolResultMessage(toolCall, toolCallID))

	client, err := s.buildClientForTenant(ctx, tenantID)
	if err != nil {
		s.failRun(ctx, tenantID, run.ID, err)
		return
	}

	// turn count isn't persisted -- resuming after approval (a human-paced
	// action) starts a fresh maxAgenticTurns budget rather than trying to
	// account precisely for turns spent before the pause. Simpler, and the
	// cap is a cost/latency guard, not a hard security boundary.
	result, err := s.driveAgentLoop(ctx, run, client, tools, routes, messages, 0)
	if err != nil {
		return // driveAgentLoop already recorded the failure on the run
	}

	_ = s.recordEvent(ctx, tenantID, run.ActorID, run.ContextType, run.ContextID, result)
}

func toolResultMessage(call *domain.AIToolCall, toolCallID string) llmclient.Message {
	if call == nil {
		return llmclient.Message{Role: llmclient.RoleTool, ToolCallID: toolCallID, Content: "error: tool call record not found"}
	}
	switch call.Status {
	case domain.ToolCallRejected:
		return llmclient.Message{Role: llmclient.RoleTool, ToolCallID: toolCallID, Content: "the analyst rejected this tool call -- proceed without its result"}
	case domain.ToolCallExecuted:
		return llmclient.Message{Role: llmclient.RoleTool, ToolCallID: toolCallID, Content: string(call.Result)}
	default: // failed, or any other unexpected status
		content := "error: tool call failed"
		if len(call.Result) > 0 {
			content = "error: " + string(call.Result)
		}
		return llmclient.Message{Role: llmclient.RoleTool, ToolCallID: toolCallID, Content: content}
	}
}

// driveAgentLoop runs turns starting from `messages`, persisting the
// conversation before every LLM call so a crash mid-loop leaves a resumable
// run rather than a stuck one. Returns the final analysis text once the
// model answers without requesting further tool calls.
func (s *AIAnalysisService) driveAgentLoop(ctx context.Context, run *domain.AIAnalysisRun, client llmclient.Client, tools []llmclient.Tool, routes map[string]agentToolRoute, messages []llmclient.Message, startTurn int) (string, error) {
	for turn := startTurn; turn < maxAgenticTurns; turn++ {
		if messagesJSON, err := json.Marshal(messages); err == nil {
			_ = s.pool.WithTenant(ctx, run.TenantID, func(tx pgx.Tx) error {
				return s.runs.SetRunning(ctx, tx, run.ID, messagesJSON)
			})
		}

		result, err := client.CompleteWithTools(ctx, analysisSystemPrompt, messages, tools)
		if err != nil {
			s.failRun(ctx, run.TenantID, run.ID, fmt.Errorf("llm analysis: %w", err))
			return "", fmt.Errorf("llm analysis: %w", err)
		}

		messages = append(messages, llmclient.Message{Role: llmclient.RoleAssistant, Content: result.Text, ToolCalls: result.ToolCalls})

		if len(result.ToolCalls) == 0 {
			messagesJSON, _ := json.Marshal(messages)
			if err := s.pool.WithTenant(ctx, run.TenantID, func(tx pgx.Tx) error {
				return s.runs.SetCompleted(ctx, tx, run.ID, messagesJSON, result.Text)
			}); err != nil {
				return "", fmt.Errorf("save completed analysis run: %w", err)
			}
			return result.Text, nil
		}

		// Only the first tool call of a multi-call turn is handled --
		// providers/models occasionally request several tools in one
		// turn, but this app's tool set is small and rarely needs
		// parallel calls; on resume the model gets exactly the one
		// result and decides what's next, same as a single-call turn.
		tc := result.ToolCalls[0]
		route, ok := routes[tc.Name]
		if !ok {
			messages = append(messages, llmclient.Message{Role: llmclient.RoleTool, ToolCallID: tc.ID, Content: "error: tool not available"})
			continue
		}

		call, err := s.mcpTools.ProposeToolCall(ctx, run.TenantID, route.ServerID, run.ContextType, run.ContextID, tc.Name, tc.Args)
		if err != nil {
			messages = append(messages, llmclient.Message{Role: llmclient.RoleTool, ToolCallID: tc.ID, Content: "error: " + err.Error()})
			continue
		}

		if route.RequiresApproval {
			messagesJSON, _ := json.Marshal(messages)
			if err := s.pool.WithTenant(ctx, run.TenantID, func(tx pgx.Tx) error {
				return s.runs.SetPaused(ctx, tx, run.ID, messagesJSON, call.ID)
			}); err != nil {
				return "", fmt.Errorf("save paused analysis run: %w", err)
			}
			return fmt.Sprintf(pausedForApprovalMessage, run.ContextType), nil
		}

		// Non-side-effecting: execute() already ran synchronously inside
		// ProposeToolCall and mirrored the outcome onto call.
		content := string(call.Result)
		if call.Status == domain.ToolCallFailed {
			content = "error: " + string(call.Result)
		}
		messages = append(messages, llmclient.Message{Role: llmclient.RoleTool, ToolCallID: tc.ID, Content: content})
	}

	err := fmt.Errorf("analysis exceeded %d tool-use turns without a final answer", maxAgenticTurns)
	s.failRun(ctx, run.TenantID, run.ID, err)
	return "", err
}

func (s *AIAnalysisService) failRun(ctx context.Context, tenantID uuid.UUID, runID int64, cause error) {
	_ = s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.SetFailed(ctx, tx, runID, cause.Error())
	})
}

// recordEvent logs the finished analysis text onto the alert/incident
// timeline -- the same ai_analysis_run event both the pre-agentic-loop
// analyzeSimple path and a completed/resumed agentic run produce.
func (s *AIAnalysisService) recordEvent(ctx context.Context, tenantID, actorID uuid.UUID, contextType string, contextID uuid.UUID, text string) error {
	data, _ := json.Marshal(map[string]string{"result": text})
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if contextType == "alert" {
			return s.alerts.InsertEvent(ctx, tx, &domain.AlertEvent{
				AlertID: contextID, TenantID: tenantID, EventType: domain.AlertEventAIAnalysisRun,
				ActorType: domain.ActorAI, ActorID: &actorID, Data: data,
			})
		}
		return s.incidents.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: contextID, TenantID: tenantID, EventType: domain.IncidentEventAIAnalysisRun,
			ActorType: domain.ActorAI, ActorID: &actorID, Data: data,
		})
	})
}

// buildClient resolves the tenant's default LLM provider and its API key,
// returning a ready-to-use llmclient.Client. tx is required (not the pool)
// because llm_providers is RLS-scoped the same as every other tenant table.
func (s *AIAnalysisService) buildClient(ctx context.Context, tx pgx.Tx) (llmclient.Client, error) {
	provider, err := s.llmProviders.GetDefault(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load default llm provider: %w", err)
	}
	if provider == nil {
		return nil, fmt.Errorf("no LLM provider configured -- set one up in Settings -> AI Integration")
	}

	apiKey, err := s.secrets.Resolve(ctx, provider.APIKeySecretRef)
	if err != nil {
		return nil, fmt.Errorf("resolve llm api key: %w", err)
	}

	client, err := llmclient.New(provider.Kind, provider.BaseURL, apiKey, provider.Model)
	if err != nil {
		return nil, fmt.Errorf("build llm client: %w", err)
	}
	return client, nil
}

// buildClientForTenant is buildClient's standalone-transaction variant, for
// ResumeAnalysisRun which doesn't already have a tx open at the point it
// needs a client.
func (s *AIAnalysisService) buildClientForTenant(ctx context.Context, tenantID uuid.UUID) (llmclient.Client, error) {
	var client llmclient.Client
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		c, err := s.buildClient(ctx, tx)
		client = c
		return err
	})
	return client, err
}

func alertPrompt(a *domain.Alert) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n", a.Title)
	fmt.Fprintf(&b, "Source: %s\n", a.Source)
	fmt.Fprintf(&b, "Severity: %s (original: %s)\n", a.Severity, a.OriginalSeverity)
	fmt.Fprintf(&b, "Status: %s\n", a.Status)
	if a.Asset != nil {
		fmt.Fprintf(&b, "Asset: %s\n", *a.Asset)
	}
	if a.SrcIP != nil {
		fmt.Fprintf(&b, "Source IP: %s\n", a.SrcIP.String())
	}
	if len(a.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(a.Tags, ", "))
	}
	if len(a.Payload) > 0 {
		fmt.Fprintf(&b, "Raw payload: %s\n", string(a.Payload))
	}
	return b.String()
}

func incidentPrompt(inc *domain.Incident) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Title: %s\n", inc.Title)
	if inc.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", inc.Description)
	}
	fmt.Fprintf(&b, "Severity: %s\n", inc.Severity)
	fmt.Fprintf(&b, "Priority: %s\n", inc.Priority)
	fmt.Fprintf(&b, "Phase: %s\n", inc.Phase)
	if len(inc.Tags) > 0 {
		fmt.Fprintf(&b, "Tags: %s\n", strings.Join(inc.Tags, ", "))
	}
	return b.String()
}
