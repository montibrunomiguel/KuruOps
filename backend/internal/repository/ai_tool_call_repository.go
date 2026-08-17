package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type AIToolCallRepository struct{}

func NewAIToolCallRepository() *AIToolCallRepository {
	return &AIToolCallRepository{}
}

const aiToolCallColumns = `
	id, tenant_id, mcp_server_id, tool_name, context_type, context_id,
	args, result, status, approved_by, approved_at, created_at`

func (r *AIToolCallRepository) Insert(ctx context.Context, tx pgx.Tx, c *domain.AIToolCall) error {
	row := tx.QueryRow(ctx, `
		insert into ai_tool_calls (tenant_id, mcp_server_id, tool_name, context_type, context_id, args, status)
		values ($1,$2,$3,$4,$5,$6,$7)
		returning id, created_at`,
		c.TenantID, c.MCPServerID, c.ToolName, c.ContextType, c.ContextID, c.Args, c.Status,
	)
	if err := row.Scan(&c.ID, &c.CreatedAt); err != nil {
		return fmt.Errorf("insert ai tool call: %w", err)
	}
	return nil
}

func (r *AIToolCallRepository) Get(ctx context.Context, tx pgx.Tx, id int64) (*domain.AIToolCall, error) {
	return queryOne(ctx, tx, `select `+aiToolCallColumns+` from ai_tool_calls where id = $1`, scanAIToolCall, id)
}

// ListPending returns tool calls awaiting analyst approval ('proposed'),
// oldest first — what an "Approvals" queue in Settings/incident detail
// would show.
func (r *AIToolCallRepository) ListPending(ctx context.Context, tx pgx.Tx) ([]domain.AIToolCall, error) {
	return queryList(ctx, tx, `
		select `+aiToolCallColumns+` from ai_tool_calls
		where status = 'proposed' order by created_at asc`, scanAIToolCall)
}

func (r *AIToolCallRepository) SetStatus(ctx context.Context, tx pgx.Tx, id int64, status domain.ToolCallStatus, approvedBy *uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		update ai_tool_calls
		set status = $2,
		    approved_by = coalesce($3, approved_by),
		    approved_at = case when $3 is not null then now() else approved_at end
		where id = $1`,
		id, status, approvedBy,
	)
	return err
}

func (r *AIToolCallRepository) SetResult(ctx context.Context, tx pgx.Tx, id int64, status domain.ToolCallStatus, result []byte) error {
	_, err := tx.Exec(ctx, `update ai_tool_calls set status = $2, result = $3 where id = $1`, id, status, result)
	return err
}

func scanAIToolCall(row pgx.Row) (*domain.AIToolCall, error) {
	var c domain.AIToolCall
	err := row.Scan(
		&c.ID, &c.TenantID, &c.MCPServerID, &c.ToolName, &c.ContextType, &c.ContextID,
		&c.Args, &c.Result, &c.Status, &c.ApprovedBy, &c.ApprovedAt, &c.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan ai tool call: %w", err)
	}
	return &c, nil
}
