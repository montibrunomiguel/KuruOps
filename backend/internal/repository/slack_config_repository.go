package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/argusops/argusops/internal/domain"
)

// SlackConfigRepository is the single-row-per-tenant "which Slack workspace
// (if any) is connected" config -- same shape as StorageConfigRepository
// (tenant_id is the row's PK, so Upsert always replaces the whole row).
type SlackConfigRepository struct{}

func NewSlackConfigRepository() *SlackConfigRepository {
	return &SlackConfigRepository{}
}

// Get joins users to resolve InstalledByUserName for display -- see
// domain.SlackConfig's doc comment. installed_by_user_id has no ON DELETE
// clause (RESTRICT), so this join can never find a missing user for an
// existing row.
func (r *SlackConfigRepository) Get(ctx context.Context, tx pgx.Tx) (*domain.SlackConfig, error) {
	var c domain.SlackConfig
	err := tx.QueryRow(ctx, `
		select sc.tenant_id, sc.bot_token_secret_ref, sc.team_id, sc.team_name, sc.bot_user_id,
		       sc.installed_by_user_id, u.name, sc.granted_scopes, sc.created_at, sc.updated_at
		from tenant_slack_config sc
		join users u on u.id = sc.installed_by_user_id
		limit 1`,
	).Scan(
		&c.TenantID, &c.BotTokenSecretRef, &c.TeamID, &c.TeamName, &c.BotUserID,
		&c.InstalledByUserID, &c.InstalledByUserName, &c.GrantedScopes, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get slack config: %w", err)
	}
	return &c, nil
}

// Upsert replaces the tenant's Slack config wholesale -- reconnecting (even
// to the same workspace) always overwrites every field, never a partial
// update.
func (r *SlackConfigRepository) Upsert(ctx context.Context, tx pgx.Tx, c *domain.SlackConfig) error {
	_, err := tx.Exec(ctx, `
		insert into tenant_slack_config (
			tenant_id, bot_token_secret_ref, team_id, team_name, bot_user_id,
			installed_by_user_id, granted_scopes
		) values ($1,$2,$3,$4,$5,$6,$7)
		on conflict (tenant_id) do update set
			bot_token_secret_ref = excluded.bot_token_secret_ref,
			team_id = excluded.team_id, team_name = excluded.team_name, bot_user_id = excluded.bot_user_id,
			installed_by_user_id = excluded.installed_by_user_id,
			granted_scopes = excluded.granted_scopes,
			updated_at = now()`,
		c.TenantID, c.BotTokenSecretRef, c.TeamID, c.TeamName, c.BotUserID,
		c.InstalledByUserID, c.GrantedScopes,
	)
	if err != nil {
		return fmt.Errorf("upsert slack config: %w", err)
	}
	return nil
}

// Delete disconnects the workspace entirely -- every Slack-dependent
// feature's "is configured" gate goes false the moment this row is gone.
func (r *SlackConfigRepository) Delete(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `delete from tenant_slack_config`)
	if err != nil {
		return fmt.Errorf("delete slack config: %w", err)
	}
	return nil
}
