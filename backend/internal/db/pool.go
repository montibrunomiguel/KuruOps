// Package db wraps the pgx pool with the tenant-scoping helper every
// repository must go through. It exists specifically so that "forgot to
// filter by tenant_id" is impossible to write: WithTenant sets the
// app.tenant_id session variable that the RLS policies in
// db/migrations/0008_row_level_security.up.sql key off of, and every query
// runs inside that transaction.
package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Pool struct {
	*pgxpool.Pool
}

func NewPool(ctx context.Context, databaseURL string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return &Pool{pool}, nil
}

// WithTenant runs fn inside a transaction that has app.tenant_id set for its
// duration, so every RLS-protected table only exposes rows for tenantID.
// set_config's third argument (is_local=true) scopes the setting to the
// transaction, so it cannot leak onto a pooled connection reused by another
// tenant's request after commit.
func (p *Pool) WithTenant(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	if _, err := tx.Exec(ctx, "select set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("set tenant context: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	return nil
}
