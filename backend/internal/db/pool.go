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

// PoolConfig overrides pgxpool's own sizing defaults -- the zero value
// (PoolConfig{}) leaves pgxpool's defaults in effect (see MaxConns/MinConns
// below), which is exactly what every test/tooling call site that doesn't
// care about pool sizing passes. See config.Config's DBPoolMaxConns/
// DBPoolMinConns doc comment for why a deployment running more than one
// replica of a binary would set these explicitly.
type PoolConfig struct {
	// MaxConns 0 leaves pgxpool's own default (the greater of 4 or
	// runtime.NumCPU()) in effect.
	MaxConns int32
	// MinConns 0 leaves pgxpool's own default (0) in effect.
	MinConns int32
}

func NewPool(ctx context.Context, databaseURL string, poolCfg PoolConfig) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}

	if poolCfg.MaxConns > 0 {
		cfg.MaxConns = poolCfg.MaxConns
	}
	if poolCfg.MinConns > 0 {
		cfg.MinConns = poolCfg.MinConns
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

// WithAdvisoryLock runs fn only if it can acquire a Postgres
// transaction-scoped advisory lock keyed by key; if another connection (a
// concurrently running replica of the same process, most likely) already
// holds it, fn is skipped and acquired is false. This is how a periodic job
// (cmd/worker's ticker loop) stays safe to run from multiple replicas
// without a separate distributed-lock service: whichever replica's tick
// gets there first does the work, the rest just skip that tick.
//
// The lock lives on its own transaction/connection for fn's whole duration
// and is released automatically on commit or rollback -- fn is free to run
// its own independent queries against the pool as it normally would (they
// use other connections), it doesn't need to go through the lock's own tx.
// pg_try_advisory_xact_lock (not the session-scoped pg_try_advisory_lock)
// is deliberate: it can never leak a held lock onto a pooled connection
// that gets handed to unrelated work later, since Postgres releases it the
// instant this transaction ends, with no separate unlock call to forget.
func (p *Pool) WithAdvisoryLock(ctx context.Context, key int64, fn func(ctx context.Context) error) (acquired bool, err error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op if already committed

	var locked bool
	if err := tx.QueryRow(ctx, "select pg_try_advisory_xact_lock($1)", key).Scan(&locked); err != nil {
		return false, fmt.Errorf("acquire advisory lock: %w", err)
	}
	if !locked {
		return false, nil
	}

	if err := fn(ctx); err != nil {
		return true, err
	}

	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit tx: %w", err)
	}

	return true, nil
}
