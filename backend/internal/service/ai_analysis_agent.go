package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
)

// The agentic loop: resolving which MCP tools a run may use, driving the
// model's turns (calling tools, pausing for analyst approval) and resuming a
// paused run. Split out of ai_analysis_service.go.

// maxAgenticTurns bounds an agentic run's total LLM round-trips, so a model
// that keeps calling tools without ever settling on an answer can't run up
// unbounded cost/latency (or, worse, loop forever across resumes).
const maxAgenticTurns = 5

// pausedForApprovalMessage is driveAgentLoop's placeholder "result" when a
// side-effecting tool call pauses the run -- the real analysis text isn't
// available yet. Only actually reaches the timeline in the (rare) case a
// resumed run pauses again: ResumeAnalysisRun records whatever
// driveAgentLoop returns as the run's event once it stops looping, paused
// or not. The normal case (an alert/incident detail page open at the time)
// learns "paused" from LatestAnalysisStatus, not from this text -- see
// domain.Alert/Incident's LatestAnalysisStatus doc comment.
const pausedForApprovalMessage = "Analysis paused: a tool call requires analyst approval before continuing (Settings -> MCP Servers -> Pending Approvals). The final result will be logged to this %s's timeline once it's approved and the analysis resumes."

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
			policy := EvaluateDiscoveredTool(server, dt)
			if !policy.Allowed {
				continue
			}
			if _, exists := routes[dt.Name]; exists {
				continue
			}
			tools = append(tools, llmclient.Tool{Name: dt.Name, Description: dt.Description, InputSchema: dt.InputSchema})
			routes[dt.Name] = agentToolRoute{
				ServerID:         server.ID,
				RequiresApproval: policy.RequiresApproval,
			}
		}
	}
	return tools, routes, nil
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
	// Fires on every return path below (decode failure, client-build
	// failure, or driveAgentLoop finishing/failing/pausing again) -- a
	// connected AlertDetailPage/IncidentDetailPage should refresh no matter
	// how this resume turned out, the same as StartAlertAnalysis/
	// StartIncidentAnalysis's background goroutine does.
	defer s.notifyAnalyzed(tenantID, run.ContextType, run.ContextID)

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

	client, err := s.BuildClient(ctx, tenantID)
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

	// Same failure mode finishSimpleRun logs, on the other path that reaches
	// recordEvent -- the earlier sweep fixed that copy and missed this one.
	// The run itself is complete and persisted by now; only the timeline
	// entry that points an analyst at it failed, so this is worth knowing
	// about but not worth failing the resumed run over.
	if err := s.recordEvent(ctx, tenantID, run.ActorID, run.ContextType, run.ContextID, result); err != nil {
		slog.Error("ai analysis: failed to record timeline event for resumed run",
			"run_id", run.ID, "context_type", run.ContextType, "context_id", run.ContextID, "error", err)
	}
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
	// Resolved once, not per turn: it costs a query, and the answer must
	// not change underneath a conversation half-way through it.
	systemPrompt := s.systemPromptFor(ctx, run, messages)
	for turn := startTurn; turn < maxAgenticTurns; turn++ {
		// This write is what makes the "resumable run" promise in the doc
		// comment above true -- if it silently fails, a crash mid-loop
		// leaves a genuinely stuck run instead of a resumable one, and
		// nothing anywhere says why. Still best-effort (a failed checkpoint
		// shouldn't abort an analysis that can otherwise finish), but no
		// longer silent.
		messagesJSON, err := json.Marshal(messages)
		if err != nil {
			slog.Error("ai analysis: failed to marshal conversation for checkpoint -- run will not be resumable from this turn",
				"run_id", run.ID, "turn", turn, "error", err)
		} else if err := s.pool.WithTenant(ctx, run.TenantID, func(tx pgx.Tx) error {
			return s.runs.SetRunning(ctx, tx, run.ID, messagesJSON)
		}); err != nil {
			slog.Error("ai analysis: failed to persist conversation checkpoint -- run will not be resumable from this turn",
				"run_id", run.ID, "turn", turn, "error", err)
		}

		result, err := client.CompleteWithTools(ctx, systemPrompt, messages, tools)
		if err != nil {
			s.failRun(ctx, run.TenantID, run.ID, fmt.Errorf("llm analysis: %w", err))
			return "", fmt.Errorf("llm analysis: %w", err)
		}

		messages = append(messages, llmclient.Message{Role: llmclient.RoleAssistant, Content: result.Text, ToolCalls: result.ToolCalls})
		s.publishTurn(run)

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
