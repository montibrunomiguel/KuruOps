package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

type MCPServerRepository struct{}

func NewMCPServerRepository() *MCPServerRepository {
	return &MCPServerRepository{}
}

const mcpServerColumns = `
	id, tenant_id, name, transport, endpoint_or_command, auth_secret_ref,
	allowed_tools, enabled_for, side_effecting_tools, is_enabled, created_by, created_at, updated_at`

func (r *MCPServerRepository) List(ctx context.Context, tx pgx.Tx) ([]domain.MCPServer, error) {
	return queryList(ctx, tx, `select `+mcpServerColumns+` from mcp_servers order by created_at asc`, scanMCPServer)
}

func (r *MCPServerRepository) Get(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.MCPServer, error) {
	return queryOne(ctx, tx, `select `+mcpServerColumns+` from mcp_servers where id = $1`, scanMCPServer, id)
}

func (r *MCPServerRepository) Insert(ctx context.Context, tx pgx.Tx, s *domain.MCPServer) error {
	row := tx.QueryRow(ctx, `
		insert into mcp_servers (
			tenant_id, name, transport, endpoint_or_command, auth_secret_ref,
			allowed_tools, enabled_for, side_effecting_tools, created_by
		) values ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		returning id, is_enabled, created_at, updated_at`,
		s.TenantID, s.Name, s.Transport, s.EndpointOrCommand, s.AuthSecretRef,
		s.AllowedTools, s.EnabledFor, s.SideEffectingTools, s.CreatedBy,
	)
	if err := row.Scan(&s.ID, &s.IsEnabled, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return fmt.Errorf("insert mcp server: %w", err)
	}
	return nil
}

func (r *MCPServerRepository) Update(ctx context.Context, tx pgx.Tx, s *domain.MCPServer) error {
	_, err := tx.Exec(ctx, `
		update mcp_servers
		set name = $2, transport = $3, endpoint_or_command = $4, auth_secret_ref = $5,
		    allowed_tools = $6, enabled_for = $7, side_effecting_tools = $8, updated_at = now()
		where id = $1`,
		s.ID, s.Name, s.Transport, s.EndpointOrCommand, s.AuthSecretRef,
		s.AllowedTools, s.EnabledFor, s.SideEffectingTools,
	)
	if err != nil {
		return fmt.Errorf("update mcp server: %w", err)
	}
	return nil
}

func (r *MCPServerRepository) SetEnabled(ctx context.Context, tx pgx.Tx, id uuid.UUID, enabled bool) error {
	_, err := tx.Exec(ctx, `update mcp_servers set is_enabled = $2, updated_at = now() where id = $1`, id, enabled)
	return err
}

func (r *MCPServerRepository) Delete(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `delete from mcp_servers where id = $1`, id)
	return err
}

func scanMCPServer(row pgx.Row) (*domain.MCPServer, error) {
	var s domain.MCPServer
	err := row.Scan(
		&s.ID, &s.TenantID, &s.Name, &s.Transport, &s.EndpointOrCommand, &s.AuthSecretRef,
		&s.AllowedTools, &s.EnabledFor, &s.SideEffectingTools, &s.IsEnabled, &s.CreatedBy, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("scan mcp server: %w", err)
	}
	return &s, nil
}
