package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/db"
	"github.com/kuruops/kuruops/internal/domain"
	"github.com/kuruops/kuruops/internal/llmclient"
	"github.com/kuruops/kuruops/internal/repository"
	"github.com/kuruops/kuruops/internal/safego"
	"github.com/kuruops/kuruops/internal/secrets"
)

// ErrAnalysisInProgress is what StartAlertAnalysis/StartIncidentAnalysis
// return when a prior analysis for the same alert/incident is still
// running or paused awaiting tool-call approval -- the handler turns this
// into 409 Conflict rather than starting a second, overlapping run.
var ErrAnalysisInProgress = errors.New("an analysis is already in progress for this item")

// ErrAutoAnalysisDisabled is what buildClient returns when the tenant's
// default LLM provider hasn't opted into domain.LLMProvider.
// AutoAnalyzeAllAlerts and this call is the unattended, fires-on-every-alert
// trigger (see AlertService.EnableAutoAnalysis) rather than an analyst
// clicking "Analyze with AI" -- cmd/ingest treats this the same as "no
// provider configured": skip quietly, not a failure worth a warn-level log.
var ErrAutoAnalysisDisabled = errors.New("auto-analysis is disabled for the default llm provider")

// AIAnalysisService runs an "Analyze with AI" request against the tenant's
// default LLM provider (see Settings -> AI Integration) and logs the result
// as an alert_events/incident_events row so it shows up in the timeline
// alongside every other change. StartAlertAnalysis/StartIncidentAnalysis
// validate the request synchronously (alert/incident exists, an LLM
// provider is configured, nothing else is already running) and return as
// soon as that's confirmed -- the actual LLM call (which can take several
// seconds, longer if it uses MCP tools) runs in its own goroutine. Callers
// learn the outcome by reloading the alert/incident (see
// domain.Alert/Incident's LatestAnalysis* fields) once notified via the
// live-update event this fires on completion or failure (see publish/
// notifyAnalyzed) -- not from a return value, since there isn't one to wait
// for anymore.
//
// When the tenant has an MCP server enabled for this analysis type (see
// domain.MCPServer.EnabledFor), analysis runs as an agentic tool-use loop
// instead of a single LLM call -- see runAgentAnalysis. A tenant with no
// MCP servers configured (the common case) gets the exact same single-call
// behavior this had before the loop existed, just also now tracked as an
// ai_analysis_runs row (see analyzeSimple) so its in-progress/failed status
// is visible the same way an agentic run's is.
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
	publish      func(tenantID uuid.UUID, eventType string, payload any)
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

// EnableEventPublishing wires a live-update notifier (events.Broadcaster.Publish
// in practice) -- see AlertService.EnableEventPublishing for the same
// post-construction-setter reasoning. Fired (via notifyAnalyzed) whenever a
// background analysis started by StartAlertAnalysis/StartIncidentAnalysis
// finishes, so a connected AlertDetailPage/IncidentDetailPage knows to
// reload and pick up the result.
func (s *AIAnalysisService) EnableEventPublishing(publish func(tenantID uuid.UUID, eventType string, payload any)) {
	s.publish = publish
}

// notifyAnalyzed reuses the same "alert"/"incident" SSE event type
// AlertService/IncidentService already publish on every other change --
// AlertDetailPage/IncidentDetailPage don't need to special-case analysis
// completion, they just refetch on any event matching the id they have
// open, the same way they already do for a status/severity change made in
// another tab.
func (s *AIAnalysisService) notifyAnalyzed(tenantID uuid.UUID, contextType string, contextID uuid.UUID) {
	if s.publish != nil {
		s.publish(tenantID, contextType, map[string]any{"id": contextID, "action": "analyzed"})
	}
}

// publishTurn fires a per-turn event as soon as the model's reply for this
// turn is appended to the conversation -- what lets the AnalysisChat
// frontend component show a new message incrementally instead of only
// finding out once the whole run finishes (see notifyAnalyzed, which still
// fires once at the very end, unchanged). One event per model reply is
// enough for the chat to render live; the AlertDetailPage/IncidentDetailPage
// "alert"/"incident" event notifyAnalyzed already fires still covers final
// status transitions.
//
// The event is a nudge, not a delivery: it says WHICH conversation changed and
// the client refetches the transcript over REST. It used to carry the model's
// whole reply as well, which nothing read (AnalysisChat only looks at
// contextId) but which cost real harm: events travel through a Postgres NOTIFY,
// capped at 8000 bytes, and a long triage report blew through that and was
// dropped, taking the live update with it. Keep this payload to identifiers.
func (s *AIAnalysisService) publishTurn(run *domain.AIAnalysisRun) {
	if s.publish == nil {
		return
	}
	s.publish(run.TenantID, "ai_analysis_turn", map[string]any{
		"contextType": run.ContextType,
		"contextId":   run.ContextID,
		"runId":       run.ID,
	})
}

// resolveAnalysisSubject loads and tag-checks the alert or incident
// (whichever contextType names) StartAlertAnalysis/StartIncidentAnalysis,
// ContinueAlertAnalysis/ContinueIncidentAnalysis, and GetAlertTranscript/
// GetIncidentTranscript all need, and returns the one piece of that load
// that actually differs between the two context types: the prompt text
// (alertPrompt/incidentPrompt). Both context types apply the exact same
// tagsVisible rule -- there is no asymmetry between them here despite some
// historical comments in this file claiming otherwise.
func (s *AIAnalysisService) resolveAnalysisSubject(ctx context.Context, tx pgx.Tx, contextType string, contextID uuid.UUID, allowedTags []string) (string, error) {
	switch contextType {
	case "alert":
		a, err := s.alerts.Get(ctx, tx, contextID)
		if err != nil {
			return "", fmt.Errorf("load alert: %w", err)
		}
		if a == nil || !tagsVisible(allowedTags, a.Tags) {
			return "", fmt.Errorf("alert %s not found", contextID)
		}
		return alertPrompt(a), nil
	case "incident":
		inc, err := s.incidents.Get(ctx, tx, contextID)
		if err != nil {
			return "", fmt.Errorf("load incident: %w", err)
		}
		if inc == nil || !tagsVisible(allowedTags, inc.Tags) {
			return "", fmt.Errorf("incident %s not found", contextID)
		}
		return incidentPrompt(inc), nil
	default:
		return "", fmt.Errorf("unknown analysis context type %q", contextType)
	}
}

// StartAlertAnalysis loads alert (subject to allowedTags, same visibility
// rule as every other AlertService method), validates there's an LLM
// provider configured and no analysis already running/paused for it,
// synchronously creates the 'running' ai_analysis_runs row (see startRun),
// then kicks off the actual LLM work in a background goroutine and
// returns. Either the single-LLM-call path (no MCP servers enabled for
// "alert_analysis") or the agentic tool-use loop (see driveAgentLoop)
// records its final result as an ai_analysis_run event once available, and
// fires notifyAnalyzed so a connected client learns to reload.
// actorID is nil for a system-triggered analysis (see
// AlertService.EnableAutoAnalysis) -- a human clicking "Analyze with AI"
// always passes their own id.
func (s *AIAnalysisService) StartAlertAnalysis(ctx context.Context, tenantID, alertID uuid.UUID, actorID *uuid.UUID, allowedTags []string) error {
	return s.startAnalysis(ctx, tenantID, "alert", alertID, actorID, allowedTags)
}

// StartIncidentAnalysis is StartAlertAnalysis's counterpart for incidents.
func (s *AIAnalysisService) StartIncidentAnalysis(ctx context.Context, tenantID, incidentID uuid.UUID, actorID *uuid.UUID, allowedTags []string) error {
	return s.startAnalysis(ctx, tenantID, "incident", incidentID, actorID, allowedTags)
}

// startAnalysis is StartAlertAnalysis/StartIncidentAnalysis's shared core --
// see resolveAnalysisSubject for the one piece that differs between the two
// context types.
func (s *AIAnalysisService) startAnalysis(ctx context.Context, tenantID uuid.UUID, contextType string, contextID uuid.UUID, actorID *uuid.UUID, allowedTags []string) error {
	var prompt string
	var client llmclient.Client
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		p, err := s.resolveAnalysisSubject(ctx, tx, contextType, contextID, allowedTags)
		if err != nil {
			return err
		}
		prompt = p

		if err := s.checkNotAlreadyRunning(ctx, tx, contextType, contextID); err != nil {
			return err
		}

		c, err := s.buildClient(ctx, tx, actorID == nil)
		if err != nil {
			return err
		}
		client = c
		return nil
	})
	if err != nil {
		return err
	}

	tools, routes, err := s.resolveAgentTools(ctx, tenantID, contextType+"_analysis")
	if err != nil {
		return err
	}

	run, messages, err := s.startRun(ctx, tenantID, actorID, contextType, contextID, tools, routes, prompt)
	if err != nil {
		return err
	}

	safego.Go("ai-analysis.startAnalysis", func() {
		bgCtx := context.Background()
		if len(tools) == 0 {
			s.finishSimpleRun(bgCtx, run, client, prompt)
		} else {
			_, _ = s.driveAgentLoop(bgCtx, run, client, tools, routes, messages, 0)
		}
		s.notifyAnalyzed(tenantID, contextType, contextID)
	})
	return nil
}

// startRun inserts the initial 'running' ai_analysis_runs row synchronously
// -- StartAlertAnalysis/StartIncidentAnalysis both call this from their
// synchronous validation path, before returning. This is what makes
// checkNotAlreadyRunning meaningful against a rapid second call: by the
// time Start*Analysis returns, the row a concurrent call would need to see
// already exists, rather than only appearing once the background goroutine
// gets around to creating it.
func (s *AIAnalysisService) startRun(ctx context.Context, tenantID uuid.UUID, actorID *uuid.UUID, contextType string, contextID uuid.UUID, tools []llmclient.Tool, routes map[string]agentToolRoute, userPrompt string) (*domain.AIAnalysisRun, []llmclient.Message, error) {
	messages := []llmclient.Message{{Role: llmclient.RoleUser, Content: userPrompt}}

	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		return nil, nil, fmt.Errorf("encode initial messages: %w", err)
	}
	toolsJSON, err := json.Marshal(tools)
	if err != nil {
		return nil, nil, fmt.Errorf("encode tools: %w", err)
	}
	routesJSON, err := json.Marshal(routes)
	if err != nil {
		return nil, nil, fmt.Errorf("encode tool routes: %w", err)
	}

	run := &domain.AIAnalysisRun{
		TenantID: tenantID, ContextType: contextType, ContextID: contextID, ActorID: actorID,
		Status: domain.AIAnalysisRunRunning, Messages: messagesJSON, Tools: toolsJSON, ToolRoutes: routesJSON,
	}
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.Insert(ctx, tx, run)
	}); err != nil {
		return nil, nil, fmt.Errorf("create analysis run: %w", err)
	}
	return run, messages, nil
}

// checkNotAlreadyRunning returns ErrAnalysisInProgress if contextID's most
// recent analysis run hasn't reached a terminal state yet -- prevents a
// second click of "Analyze with AI" (or a race between auto-analysis and a
// manual click) from starting an overlapping run against the same
// conversation history.
func (s *AIAnalysisService) checkNotAlreadyRunning(ctx context.Context, tx pgx.Tx, contextType string, contextID uuid.UUID) error {
	run, err := s.runs.LatestRun(ctx, tx, contextType, contextID)
	if err != nil {
		return fmt.Errorf("check existing analysis: %w", err)
	}
	return blockIfRunning(run)
}

// blockIfRunning is checkNotAlreadyRunning's pure check, split out so
// Continue*Analysis can reuse it against a run it already loaded (to also
// branch on "is the latest run completed" -- see continueRun) instead of
// querying LatestRun a second time.
func blockIfRunning(run *domain.AIAnalysisRun) error {
	if run != nil && (run.Status == domain.AIAnalysisRunRunning || run.Status == domain.AIAnalysisRunPaused) {
		return ErrAnalysisInProgress
	}
	return nil
}

// finishSimpleRun is the background-goroutine continuation of the
// pre-agentic-loop behavior: one LLM call, no tools. Kept as its own path
// (rather than routing everything through the agentic loop with zero
// tools) so the common case -- no MCP servers configured -- has exactly
// the same shape it always did. run already exists (see startRun, called
// synchronously before this goroutine was spawned) -- this only ever
// updates it to completed/failed, never creates it.
func (s *AIAnalysisService) finishSimpleRun(ctx context.Context, run *domain.AIAnalysisRun, client llmclient.Client, userPrompt string) {
	// Only ever called for a run whose conversation is the single seeded
	// context message (see startAnalysis), so no question has been asked yet.
	seeded := []llmclient.Message{{Role: llmclient.RoleUser, Content: userPrompt}}
	text, err := client.Complete(ctx, s.systemPromptFor(ctx, run, seeded), userPrompt)
	if err != nil {
		s.failRun(ctx, run.TenantID, run.ID, err)
		return
	}

	finalMessages, _ := json.Marshal([]llmclient.Message{
		{Role: llmclient.RoleUser, Content: userPrompt},
		{Role: llmclient.RoleAssistant, Content: text},
	})
	if err := s.pool.WithTenant(ctx, run.TenantID, func(tx pgx.Tx) error {
		return s.runs.SetCompleted(ctx, tx, run.ID, finalMessages, text)
	}); err != nil {
		// The LLM call itself succeeded -- only persisting its result
		// failed. Left un-logged, this run stays stuck showing "running"
		// forever with no trace anywhere of why, and no one to notice
		// short of an analyst eventually asking "why did my analysis never
		// finish".
		slog.Error("ai analysis: failed to persist completed run", "run_id", run.ID, "context_type", run.ContextType, "context_id", run.ContextID, "error", err)
		return
	}

	if err := s.recordEvent(ctx, run.TenantID, run.ActorID, run.ContextType, run.ContextID, text); err != nil {
		// The run itself completed and is persisted (SetCompleted above
		// already succeeded) -- only the timeline entry that surfaces it to
		// an analyst failed. Not worth failing the whole run over, but
		// worth knowing about: without this, the analysis result exists in
		// the database but nothing in the alert/incident timeline ever
		// points an analyst at it.
		slog.Error("ai analysis: failed to record timeline event", "run_id", run.ID, "context_type", run.ContextType, "context_id", run.ContextID, "error", err)
	}
}

func (s *AIAnalysisService) failRun(ctx context.Context, tenantID uuid.UUID, runID int64, cause error) {
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.SetFailed(ctx, tx, runID, cause.Error())
	}); err != nil {
		// cause is already the reason the run failed in the first place --
		// this is a second, independent failure (persisting that fact), so
		// both need to be visible: without this log line, a run stuck
		// "running" forever from a failed SetFailed write is
		// indistinguishable from finishSimpleRun's own silent-failure case
		// above, and just as hard to diagnose after the fact.
		slog.Error("ai analysis: failed to persist failed run", "run_id", runID, "cause", cause, "error", err)
	}
}

// recordEvent logs the finished analysis text onto the alert/incident
// timeline -- the same ai_analysis_run event both the pre-agentic-loop
// analyzeSimple path and a completed/resumed agentic run produce.
func (s *AIAnalysisService) recordEvent(ctx context.Context, tenantID uuid.UUID, actorID *uuid.UUID, contextType string, contextID uuid.UUID, text string) error {
	data, _ := json.Marshal(map[string]string{"result": text})
	return s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		if contextType == "alert" {
			return s.alerts.InsertEvent(ctx, tx, &domain.AlertEvent{
				AlertID: contextID, TenantID: tenantID, EventType: domain.AlertEventAIAnalysisRun,
				ActorType: domain.ActorAI, ActorID: actorID, Data: data,
			})
		}
		return s.incidents.InsertEvent(ctx, tx, &domain.IncidentEvent{
			IncidentID: contextID, TenantID: tenantID, EventType: domain.IncidentEventAIAnalysisRun,
			ActorType: domain.ActorAI, ActorID: actorID, Data: data,
		})
	})
}

// buildClient resolves the tenant's default LLM provider and its API key,
// returning a ready-to-use llmclient.Client. tx is required (not the pool)
// because llm_providers is RLS-scoped the same as every other tenant table.
// autoTriggered must be true only for AlertService's unattended ingest-time
// call (actorID nil in startAnalysis) -- an analyst clicking "Analyze with
// AI" always proceeds regardless of AutoAnalyzeAllAlerts, since that flag
// only gates the fires-on-every-alert path, never the explicit one.
func (s *AIAnalysisService) buildClient(ctx context.Context, tx pgx.Tx, autoTriggered bool) (llmclient.Client, error) {
	provider, err := s.llmProviders.GetDefault(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("load default llm provider: %w", err)
	}
	if provider == nil {
		return nil, fmt.Errorf("no LLM provider configured -- set one up in Settings -> AI Integration")
	}
	if autoTriggered && !provider.AutoAnalyzeAllAlerts {
		return nil, ErrAutoAnalysisDisabled
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

// BuildClient is buildClient's standalone-transaction variant, for callers
// that don't already have a tx open at the point they need an LLM client --
// ResumeAnalysisRun (below) and PostmortemService.Generate, which reuses
// this tenant's configured LLM provider rather than resolving its own.
// Exported for that second, cross-service caller; every other use of the
// unexported buildClient stays internal to this file.
func (s *AIAnalysisService) BuildClient(ctx context.Context, tenantID uuid.UUID) (llmclient.Client, error) {
	var client llmclient.Client
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		// false: both callers (ResumeAnalysisRun continuing an
		// already-started run, PostmortemService.Generate) are past the
		// point AutoAnalyzeAllAlerts gates, or are themselves an explicit
		// action -- never the unattended ingest-time trigger.
		c, err := s.buildClient(ctx, tx, false)
		client = c
		return err
	})
	return client, err
}
