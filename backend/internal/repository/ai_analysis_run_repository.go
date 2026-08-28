package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/kuruops/kuruops/internal/domain"
)

type AIAnalysisRunRepository struct{}

func NewAIAnalysisRunRepository() *AIAnalysisRunRepository {
	return &AIAnalysisRunRepository{}
}

const aiAnalysisRunColumns = `
	id, tenant_id, context_type, context_id, actor_id, status,
	messages, tools, tool_routes, pending_tool_call_id, result, error, created_at, updated_at`

func (r *AIAnalysisRunRepository) Insert(ctx context.Context, tx pgx.Tx, run *domain.AIAnalysisRun) error {
	row := tx.QueryRow(ctx, `
		insert into ai_analysis_runs (tenant_id, context_type, context_id, actor_id, status, messages, tools, tool_routes)
		values ($1,$2,$3,$4,$5,$6,$7,$8)
		returning id, created_at, updated_at`,
		run.TenantID, run.ContextType, run.ContextID, run.ActorID, run.Status,
		run.Messages, run.Tools, run.ToolRoutes,
	)
	if err := row.Scan(&run.ID, &run.CreatedAt, &run.UpdatedAt); err != nil {
		return fmt.Errorf("insert ai analysis run: %w", err)
	}
	return nil
}

func (r *AIAnalysisRunRepository) Get(ctx context.Context, tx pgx.Tx, id int64) (*domain.AIAnalysisRun, error) {
	row := tx.QueryRow(ctx, `select `+aiAnalysisRunColumns+` from ai_analysis_runs where id = $1`, id)
	return scanAIAnalysisRun(row)
}

// ListByContext returns every analysis run for one alert/incident, most
// recent first -- backs a possible future "analysis history" view, and
// lets tests inspect the outcome of an agentic run without the service
// layer needing to leak run IDs through AnalyzeAlert/AnalyzeIncident's
// public (string, error) signature.
func (r *AIAnalysisRunRepository) ListByContext(ctx context.Context, tx pgx.Tx, contextType string, contextID uuid.UUID) ([]domain.AIAnalysisRun, error) {
	return queryList(ctx, tx, `
		select `+aiAnalysisRunColumns+` from ai_analysis_runs
		where context_type = $1 and context_id = $2
		order by created_at desc`,
		scanAIAnalysisRun, contextType, contextID,
	)
}

// LatestRun returns the single most recent analysis run for one
// alert/incident, regardless of status, or nil if none exists yet -- backs
// AlertService/IncidentService's Get (see domain.Alert/Incident's
// LatestAnalysis/LatestAnalysisStatus/LatestAnalysisError), so a caller can
// tell "still running", "the last one failed", and "here's the completed
// result" apart instead of only ever seeing a result or nothing.
func (r *AIAnalysisRunRepository) LatestRun(ctx context.Context, tx pgx.Tx, contextType string, contextID uuid.UUID) (*domain.AIAnalysisRun, error) {
	row := tx.QueryRow(ctx, `
		select `+aiAnalysisRunColumns+` from ai_analysis_runs
		where context_type = $1 and context_id = $2
		order by created_at desc limit 1`,
		contextType, contextID,
	)
	run, err := scanAIAnalysisRun(row)
	if err != nil {
		return nil, fmt.Errorf("query latest analysis run: %w", err)
	}
	return run, nil
}

// GetPausedByToolCall finds the run (if any) waiting on callID -- how a
// tool-call approval finds its way back to the conversation it belongs to.
// nil, nil (not an error) when no run is waiting on it, which is the normal
// case for a tool call proposed outside any agentic run.
func (r *AIAnalysisRunRepository) GetPausedByToolCall(ctx context.Context, tx pgx.Tx, callID int64) (*domain.AIAnalysisRun, error) {
	row := tx.QueryRow(ctx, `
		select `+aiAnalysisRunColumns+` from ai_analysis_runs
		where pending_tool_call_id = $1 and status = 'paused'`, callID)
	return scanAIAnalysisRun(row)
}

// SetPaused stops the run at the given conversation state, waiting on
// toolCallID's approval/rejection to resume (see
// service.MCPToolService.ApproveToolCall / RejectToolCall).
func (r *AIAnalysisRunRepository) SetPaused(ctx context.Context, tx pgx.Tx, id int64, messages []byte, toolCallID int64) error {
	_, err := tx.Exec(ctx, `
		update ai_analysis_runs
		set status = 'paused', messages = $2, pending_tool_call_id = $3, updated_at = now()
		where id = $1`,
		id, messages, toolCallID,
	)
	return err
}

// SetRunning updates the conversation state mid-loop (each turn) without
// changing status -- called before every CompleteWithTools call so a crash
// mid-loop leaves the run resumable from its last completed turn rather
// than losing the whole conversation.
func (r *AIAnalysisRunRepository) SetRunning(ctx context.Context, tx pgx.Tx, id int64, messages []byte) error {
	_, err := tx.Exec(ctx, `
		update ai_analysis_runs
		set status = 'running', messages = $2, pending_tool_call_id = null, updated_at = now()
		where id = $1`,
		id, messages,
	)
	return err
}

// AppendUserMessage persists a conversation that just grew by one analyst
// turn (see AIAnalysisService.Continue*Analysis, continuing a completed
// run) -- same shape as SetRunning, but named for its own call site since
// "running" here means "resuming a finished conversation", not "mid-loop".
func (r *AIAnalysisRunRepository) AppendUserMessage(ctx context.Context, tx pgx.Tx, id int64, messages []byte) error {
	_, err := tx.Exec(ctx, `
		update ai_analysis_runs
		set status = 'running', messages = $2, pending_tool_call_id = null, updated_at = now()
		where id = $1`,
		id, messages,
	)
	return err
}

func (r *AIAnalysisRunRepository) SetCompleted(ctx context.Context, tx pgx.Tx, id int64, messages []byte, result string) error {
	_, err := tx.Exec(ctx, `
		update ai_analysis_runs
		set status = 'completed', messages = $2, result = $3, pending_tool_call_id = null, updated_at = now()
		where id = $1`,
		id, messages, result,
	)
	return err
}

func (r *AIAnalysisRunRepository) SetFailed(ctx context.Context, tx pgx.Tx, id int64, cause string) error {
	_, err := tx.Exec(ctx, `
		update ai_analysis_runs
		set status = 'failed', error = $2, pending_tool_call_id = null, updated_at = now()
		where id = $1`,
		id, cause,
	)
	return err
}

func scanAIAnalysisRun(row pgx.Row) (*domain.AIAnalysisRun, error) {
	var run domain.AIAnalysisRun
	err := row.Scan(
		&run.ID, &run.TenantID, &run.ContextType, &run.ContextID, &run.ActorID, &run.Status,
		&run.Messages, &run.Tools, &run.ToolRoutes, &run.PendingToolCallID, &run.Result, &run.Error,
		&run.CreatedAt, &run.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan ai analysis run: %w", err)
	}
	return &run, nil
}
