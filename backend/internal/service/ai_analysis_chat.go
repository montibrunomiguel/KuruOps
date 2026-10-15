package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/safego"
)

// The analyst-facing side of AI analysis: the chat transcript wire types and
// reads, and the follow-up path (Continue*Analysis) that appends an analyst's
// message to a finished run, including undoing it when the provider fails.
// Split out of ai_analysis_service.go.

// ChatToolCall/ChatMessage/ChatTranscript are the JSON-friendly wire shape
// for AnalysisChat's GET/streamed messages -- llmclient.Message itself has
// no json tags (it was never meant to leave the backend, see its doc
// comment), so handlers never marshal it directly; everything crossing the
// HTTP boundary goes through these instead.
type ChatToolCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type ChatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []ChatToolCall `json:"toolCalls,omitempty"`
	ToolCallID string         `json:"toolCallId,omitempty"`
}

// ChatTranscript is GetAlertTranscript/GetIncidentTranscript's response --
// the run's full conversation (minus the synthetic seed prompt, see
// buildTranscript) plus enough status to drive the chat UI: whether it's
// mid-turn, paused on a tool-call approval, or done.
type ChatTranscript struct {
	RunID             int64         `json:"runId,omitempty"`
	Status            string        `json:"status,omitempty"`
	Messages          []ChatMessage `json:"messages"`
	PendingToolCallID *int64        `json:"pendingToolCallId,omitempty"`
	Error             *string       `json:"error,omitempty"`
}

func toChatMessage(m llmclient.Message) ChatMessage {
	cm := ChatMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
	for _, tc := range m.ToolCalls {
		cm.ToolCalls = append(cm.ToolCalls, ChatToolCall{ID: tc.ID, Name: tc.Name, Args: tc.Args})
	}
	return cm
}

// buildTranscript always drops the conversation's first message -- every
// run (Start*Analysis or continueRun's fresh-run branch) seeds messages[0]
// with the auto-generated alert/incident context dump (see alertPrompt/
// incidentPrompt), never something an analyst actually typed. Showing that
// as a "user" chat bubble would surface internal prompt plumbing the old
// static result view never did either (it only ever rendered the final
// assistant text) -- stripping it keeps the chat's first visible message
// the same thing the analyst has always seen: either their own question, or
// (for a run auto-triggered on ingest, never continued yet) the model's
// answer.
func buildTranscript(run *domain.AIAnalysisRun) (*ChatTranscript, error) {
	if run == nil {
		return &ChatTranscript{Messages: []ChatMessage{}}, nil
	}
	var messages []llmclient.Message
	if err := json.Unmarshal(run.Messages, &messages); err != nil {
		return nil, fmt.Errorf("decode conversation: %w", err)
	}
	if len(messages) > 0 {
		messages = messages[1:]
	}
	wire := make([]ChatMessage, 0, len(messages))
	for _, m := range messages {
		wire = append(wire, toChatMessage(m))
	}
	return &ChatTranscript{
		RunID: run.ID, Status: string(run.Status), Messages: wire,
		PendingToolCallID: run.PendingToolCallID, Error: run.Error,
	}, nil
}

// GetAlertTranscript backs AnalysisChat's initial load and its
// refetch-on-SSE-event -- same visibility rule as StartAlertAnalysis (must
// exist, must be tag-visible).
func (s *AIAnalysisService) GetAlertTranscript(ctx context.Context, tenantID, alertID uuid.UUID, allowedTags []string) (*ChatTranscript, error) {
	return s.getTranscript(ctx, tenantID, "alert", alertID, allowedTags)
}

// GetIncidentTranscript is GetAlertTranscript's counterpart for incidents.
func (s *AIAnalysisService) GetIncidentTranscript(ctx context.Context, tenantID, incidentID uuid.UUID, allowedTags []string) (*ChatTranscript, error) {
	return s.getTranscript(ctx, tenantID, "incident", incidentID, allowedTags)
}

// getTranscript is GetAlertTranscript/GetIncidentTranscript's shared core --
// see resolveAnalysisSubject for the one piece that differs between the two
// context types.
func (s *AIAnalysisService) getTranscript(ctx context.Context, tenantID uuid.UUID, contextType string, contextID uuid.UUID, allowedTags []string) (*ChatTranscript, error) {
	var transcript *ChatTranscript
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if _, err := s.resolveAnalysisSubject(ctx, tx, contextType, contextID, allowedTags); err != nil {
			return err
		}
		run, err := s.runs.LatestRun(ctx, tx, contextType, contextID)
		if err != nil {
			return fmt.Errorf("load latest analysis: %w", err)
		}
		transcript, err = buildTranscript(run)
		return err
	})
	return transcript, err
}

// ContinueAlertAnalysis is what the "Analyze with AI" chat's message box
// calls -- unlike StartAlertAnalysis (always a fresh run), this continues
// whatever conversation is already there: appends to a completed run's
// existing messages, or seeds a fresh one (same as Start*Analysis) if none
// exists yet or the last one failed. Still rejects outright if the latest
// run is running/paused -- never two loop instances driving the same run.
func (s *AIAnalysisService) ContinueAlertAnalysis(ctx context.Context, tenantID, alertID, actorID uuid.UUID, allowedTags []string, text string) error {
	return s.continueAnalysis(ctx, tenantID, "alert", alertID, actorID, allowedTags, text)
}

// ContinueIncidentAnalysis is ContinueAlertAnalysis's counterpart for
// incidents -- see that method's doc comment.
func (s *AIAnalysisService) ContinueIncidentAnalysis(ctx context.Context, tenantID, incidentID, actorID uuid.UUID, allowedTags []string, text string) error {
	return s.continueAnalysis(ctx, tenantID, "incident", incidentID, actorID, allowedTags, text)
}

// continueAnalysis is ContinueAlertAnalysis/ContinueIncidentAnalysis's
// shared core -- see resolveAnalysisSubject for the one piece that differs
// between the two context types.
func (s *AIAnalysisService) continueAnalysis(ctx context.Context, tenantID uuid.UUID, contextType string, contextID, actorID uuid.UUID, allowedTags []string, text string) error {
	var prompt string
	var client llmclient.Client
	var latest *domain.AIAnalysisRun
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		p, err := s.resolveAnalysisSubject(ctx, tx, contextType, contextID, allowedTags)
		if err != nil {
			return err
		}
		prompt = p

		r, err := s.runs.LatestRun(ctx, tx, contextType, contextID)
		if err != nil {
			return fmt.Errorf("check existing analysis: %w", err)
		}
		if err := blockIfRunning(r); err != nil {
			return err
		}
		latest = r

		// false: continuing an analysis chat is always an explicit analyst
		// action (actorID here is a plain uuid.UUID, never nil), never the
		// unattended ingest-time trigger.
		c, err := s.buildClient(ctx, tx, false)
		if err != nil {
			return err
		}
		client = c
		return nil
	})
	if err != nil {
		return err
	}

	run, snapshot, messages, tools, routes, err := s.continueRun(ctx, tenantID, actorID, contextType, contextID, latest, prompt, text)
	if err != nil {
		return err
	}

	safego.Go("ai-analysis.continueAnalysis", func() {
		bgCtx := context.Background()
		if _, err := s.driveAgentLoop(bgCtx, run, client, tools, routes, messages, 0); err != nil {
			s.restoreCompletedRun(bgCtx, tenantID, snapshot, err)
		}
		s.notifyAnalyzed(tenantID, contextType, contextID)
	})
	return nil
}

// restoreCompletedRun puts a finished analysis back after a follow-up
// question failed to get an answer.
//
// Without this, one flaky provider call destroyed the work: asking a
// question re-uses the completed run (see continueRun), so a 503 on that
// turn left the run 'failed' -- and the next attempt, seeing a failed
// latest run, started a brand new conversation from scratch. Two clicks
// after a finished triage report, the analyst's screen showed nothing but
// their own unanswered question, and an LLM 503 is the most ordinary
// failure there is.
//
// Dropping the unanswered turn is deliberate: leaving it in the transcript
// would show a question the model never replied to, which reads as a
// silent failure. The cause is kept on the run's error field (the run is
// 'completed', so the chat is what shows it -- see AnalysisChat) so the
// analyst learns why their question disappeared and can ask again.
func (s *AIAnalysisService) restoreCompletedRun(ctx context.Context, tenantID uuid.UUID, snapshot *completedRunSnapshot, cause error) {
	if snapshot == nil {
		// A fresh run that failed is simply a failed run -- there is no
		// earlier good state to go back to.
		return
	}
	result := ""
	if snapshot.result != nil {
		result = *snapshot.result
	}
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.RestoreCompleted(ctx, tx, snapshot.runID, snapshot.messages, result, cause.Error())
	}); err != nil {
		slog.Error("ai analysis: could not restore the completed analysis after a failed follow-up -- the report may be hidden behind a failed run",
			"run_id", snapshot.runID, "cause", cause, "error", err)
	}
}

// completedRunSnapshot is what a finished analysis looked like before an
// analyst's follow-up question was appended to it -- see continueRun's
// second return value and restoreCompletedRun.
type completedRunSnapshot struct {
	runID    int64
	messages json.RawMessage
	result   *string
}

// continueRun is ContinueAlertAnalysis/ContinueIncidentAnalysis's shared
// core, once the caller has already confirmed latest isn't running/paused.
// Two cases: latest is 'completed' -- load its saved messages/tools/routes
// (same tools the conversation already used, not a re-resolve, mirroring
// ResumeAnalysisRun) and append text as the next user turn; otherwise (no
// run yet, or the last one failed) -- a fresh run, seeded the same way
// startRun does but with the analyst's text as an immediate second user
// turn rather than waiting for the model to ask for one.
//
// The second return value is non-nil only in the first case, and is how a
// failed follow-up gets undone: see restoreCompletedRun.
func (s *AIAnalysisService) continueRun(ctx context.Context, tenantID, actorID uuid.UUID, contextType string, contextID uuid.UUID, latest *domain.AIAnalysisRun, prompt, text string) (*domain.AIAnalysisRun, *completedRunSnapshot, []llmclient.Message, []llmclient.Tool, map[string]agentToolRoute, error) {
	if latest != nil && latest.Status == domain.AIAnalysisRunCompleted {
		var messages []llmclient.Message
		var tools []llmclient.Tool
		var routes map[string]agentToolRoute
		if err := json.Unmarshal(latest.Messages, &messages); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("decode saved conversation: %w", err)
		}
		if err := json.Unmarshal(latest.Tools, &tools); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("decode saved tools: %w", err)
		}
		if err := json.Unmarshal(latest.ToolRoutes, &routes); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("decode saved tool routes: %w", err)
		}
		// Kept before the follow-up turn is appended, so a provider failure
		// can put the finished analysis back exactly as it was.
		priorMessages := latest.Messages
		messages = append(messages, llmclient.Message{Role: llmclient.RoleUser, Content: text})
		messagesJSON, err := json.Marshal(messages)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("encode messages: %w", err)
		}
		if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
			return s.runs.AppendUserMessage(ctx, tx, latest.ID, messagesJSON)
		}); err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("append user message: %w", err)
		}
		snapshot := &completedRunSnapshot{runID: latest.ID, messages: priorMessages, result: latest.Result}
		latest.Messages = messagesJSON
		latest.Status = domain.AIAnalysisRunRunning
		return latest, snapshot, messages, tools, routes, nil
	}

	tools, routes, err := s.resolveAgentTools(ctx, tenantID, contextType+"_analysis")
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	messages := []llmclient.Message{{Role: llmclient.RoleUser, Content: prompt}, {Role: llmclient.RoleUser, Content: text}}
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("encode initial messages: %w", err)
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("encode tools: %w", err)
	}
	routesJSON, err := json.Marshal(routes)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("encode tool routes: %w", err)
	}
	run := &domain.AIAnalysisRun{
		TenantID: tenantID, ContextType: contextType, ContextID: contextID, ActorID: &actorID,
		Status: domain.AIAnalysisRunRunning, Messages: messagesJSON, Tools: toolsJSON, ToolRoutes: routesJSON,
	}
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.Insert(ctx, tx, run)
	}); err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("create analysis run: %w", err)
	}
	return run, nil, messages, tools, routes, nil
}
