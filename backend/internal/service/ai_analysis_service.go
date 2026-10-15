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
// trigger (see AlertService.EnableAutoAnalysisQueue) rather than an analyst
// clicking "Analyze with AI" -- the analysis queue treats this the same as "no
// provider configured": the job is marked skipped, not retried and not a
// failure worth a warn-level log.
var ErrAutoAnalysisDisabled = errors.New("auto-analysis is disabled for the default llm provider")

// ErrNoLLMProvider is returned when the tenant has no default LLM provider at
// all. A sentinel (it used to be an anonymous error that cmd/ingest matched by
// substring) so the analysis queue can recognise "nothing to do, and nothing
// will change by retrying" without parsing a message.
var ErrNoLLMProvider = errors.New("no LLM provider configured -- set one up in Settings -> AI Integration")

// ErrSubjectNotFound is what resolveAnalysisSubject's "alert/incident ... not
// found" errors match with errors.Is. The text is unchanged -- handlers show it
// to the caller -- so this is a type with an Is method rather than a wrapped
// sentinel, which would have appended to the message.
var ErrSubjectNotFound = errors.New("analysis subject not found")

type subjectNotFoundError struct{ msg string }

func (e *subjectNotFoundError) Error() string        { return e.msg }
func (e *subjectNotFoundError) Is(target error) bool { return target == ErrSubjectNotFound }

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
			return "", &subjectNotFoundError{fmt.Sprintf("alert %s not found", contextID)}
		}
		return alertPrompt(a), nil
	case "incident":
		inc, err := s.incidents.Get(ctx, tx, contextID)
		if err != nil {
			return "", fmt.Errorf("load incident: %w", err)
		}
		if inc == nil || !tagsVisible(allowedTags, inc.Tags) {
			return "", &subjectNotFoundError{fmt.Sprintf("incident %s not found", contextID)}
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
// AlertService.EnableAutoAnalysisQueue) -- a human clicking "Analyze with AI"
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
// context types. It validates and persists the run synchronously, then hands
// the LLM work to a background goroutine and returns.
func (s *AIAnalysisService) startAnalysis(ctx context.Context, tenantID uuid.UUID, contextType string, contextID uuid.UUID, actorID *uuid.UUID, allowedTags []string) error {
	a, err := s.prepareAnalysis(ctx, tenantID, contextType, contextID, actorID, allowedTags)
	if err != nil {
		return err
	}

	safego.Go("ai-analysis.startAnalysis", func() {
		// context.Background(), not ctx: the request that started this has
		// already returned and had its context cancelled.
		_ = s.execute(context.Background(), a)
		s.notifyAnalyzed(tenantID, contextType, contextID)
	})
	return nil
}

// RunAutoAnalysis performs the automatic analysis of one freshly ingested alert
// and returns once it is finished, with the outcome as the error. It is what the
// analysis queue (AIAnalysisQueue) calls for each job.
//
// It is the synchronous counterpart of StartAlertAnalysis, which returns the
// moment the run is persisted and reports nothing about how it ends -- fine for
// a button an analyst clicks, useless for a queue that has to decide between
// "done", "try again in a few minutes" and "give up". Same validation, same run,
// same prompts; the only differences are that it is always the unattended
// trigger (no actor, so the provider's auto-analysis setting is enforced and the
// triage prompt applies -- see systemPromptFor) and that it blocks.
//
// The returned error is classified by the caller with llmclient.IsRetryable and
// the Err* sentinels: ErrAutoAnalysisDisabled, ErrNoLLMProvider and
// ErrSubjectNotFound mean there is nothing to do; ErrAnalysisInProgress means
// try again later.
func (s *AIAnalysisService) RunAutoAnalysis(ctx context.Context, tenantID, alertID uuid.UUID) error {
	a, err := s.prepareAnalysis(ctx, tenantID, "alert", alertID, nil, nil)
	if err != nil {
		return err
	}
	err = s.execute(ctx, a)
	s.notifyAnalyzed(tenantID, "alert", alertID)
	return err
}

// analysis is a validated analysis whose run row exists but whose LLM work has
// not started: everything execute needs.
type analysis struct {
	run      *domain.AIAnalysisRun
	client   llmclient.Client
	tools    []llmclient.Tool
	routes   map[string]agentToolRoute
	messages []llmclient.Message
	prompt   string
}

// prepareAnalysis is the synchronous half of starting an analysis: load and
// access-check the subject, refuse if one is already running, build the LLM
// client, resolve the MCP tools, and persist the 'running' run row. The row is
// created here, before anyone starts executing, which is what makes
// checkNotAlreadyRunning meaningful against a rapid second call.
//
// actorID nil marks the unattended trigger and is what buildClient uses to
// enforce the provider's auto-analysis setting.
func (s *AIAnalysisService) prepareAnalysis(ctx context.Context, tenantID uuid.UUID, contextType string, contextID uuid.UUID, actorID *uuid.UUID, allowedTags []string) (*analysis, error) {
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
		return nil, err
	}

	tools, routes, err := s.resolveAgentTools(ctx, tenantID, contextType+"_analysis")
	if err != nil {
		return nil, err
	}

	run, messages, err := s.startRun(ctx, tenantID, actorID, contextType, contextID, tools, routes, prompt)
	if err != nil {
		return nil, err
	}
	return &analysis{run: run, client: client, tools: tools, routes: routes, messages: messages, prompt: prompt}, nil
}

// execute runs the LLM work for a prepared analysis to completion and returns how
// it ended: nil on success (including a run that paused awaiting an analyst's
// approval of a tool call, which is waiting, not failed), otherwise the error
// that already marked the run failed. Either path through it -- the single
// call, or the agentic loop when MCP tools are enabled -- records the run's
// outcome itself; callers add only the notification.
func (s *AIAnalysisService) execute(ctx context.Context, a *analysis) error {
	if len(a.tools) == 0 {
		return s.finishSimpleRun(ctx, a.run, a.client, a.prompt)
	}
	_, err := s.driveAgentLoop(ctx, a.run, a.client, a.tools, a.routes, a.messages, 0)
	return err
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
//
// It returns how the run ended (nil on success), which the background
// goroutine ignores and the analysis queue acts on: the error wraps the
// provider's, so llmclient.IsRetryable can tell a blip from a bad API key.
func (s *AIAnalysisService) finishSimpleRun(ctx context.Context, run *domain.AIAnalysisRun, client llmclient.Client, userPrompt string) error {
	// Only ever called for a run whose conversation is the single seeded
	// context message (see prepareAnalysis), so no question has been asked yet.
	seeded := []llmclient.Message{{Role: llmclient.RoleUser, Content: userPrompt}}
	text, err := client.Complete(ctx, s.systemPromptFor(ctx, run, seeded), userPrompt)
	if err != nil {
		// Same "llm analysis: " prefix the agentic loop records, so a failed
		// run reads the same whichever path produced it.
		err = fmt.Errorf("llm analysis: %w", err)
		s.failRun(ctx, run.TenantID, run.ID, err)
		return err
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
		return fmt.Errorf("save completed analysis run: %w", err)
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
	return nil
}

func (s *AIAnalysisService) failRun(ctx context.Context, tenantID uuid.UUID, runID int64, cause error) {
	if err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		return s.runs.SetFailed(ctx, tx, runID, cause.Error(), llmclient.ErrorCode(cause))
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
		return nil, ErrNoLLMProvider
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

// AbandonInterruptedRuns fails the 'running' runs an alert's previous, dead
// consumer left behind, and reports how many. The analysis queue calls it when it
// takes over a job whose lease ran out: the consumer holding that lease stopped
// renewing it -- it crashed, was killed in a deploy, or lost its database
// connection -- so the run it was driving will never finish, and while it stays
// 'running' it makes checkNotAlreadyRunning refuse the retry. Without this the
// retry waits for the worker's stale-run reaper (15 minutes) for no reason.
//
// Safe because a lapsed lease is exactly the evidence that nothing is driving the
// run any more; a consumer that was only stalled and wakes up finds its fenced
// writes rejected (see AIAnalysisJobRepository) and stops.
func (s *AIAnalysisService) AbandonInterruptedRuns(ctx context.Context, tenantID, alertID uuid.UUID) (int64, error) {
	var n int64
	err := s.pool.WithTenant(ctx, tenantID, func(tx pgx.Tx) error {
		var err error
		n, err = s.runs.FailRunning(ctx, tx, "alert", alertID,
			"interrupted: the process running this analysis stopped before it finished; it was retried")
		return err
	})
	return n, err
}
