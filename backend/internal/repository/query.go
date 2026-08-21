package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// queryOne runs sql expecting at most one row, using scan to turn it into a
// *T -- scan is expected to follow this package's convention of returning
// nil, nil on pgx.ErrNoRows (see e.g. scanMCPServer) rather than surfacing
// "no rows" as an error.
func queryOne[T any](ctx context.Context, tx pgx.Tx, sql string, scan func(pgx.Row) (*T, error), args ...any) (*T, error) {
	return scan(tx.QueryRow(ctx, sql, args...))
}

// queryList runs sql and scans every row with scan, collapsing the
// query+defer rows.Close()+for rows.Next()+scan+append+rows.Err() loop
// repeated across this package's List-style methods. scan receives each
// pgx.Rows directly -- pgx.Rows satisfies pgx.Row's Scan(...any) error
// signature, so the same scan func used by queryOne works here too.
func queryList[T any](ctx context.Context, tx pgx.Tx, sql string, scan func(pgx.Row) (*T, error), args ...any) ([]T, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	items := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, rows.Err()
}

// replaceChildRows collapses the "delete every existing child row for one
// parent, then reinsert the caller's current set" idiom repeated near-
// identically across OnCallScheduleRepository.replaceParticipants/
// replaceWorkingHours, EscalationPolicyRepository.replaceSteps, and
// PlaybookRepository.replaceSteps -- a parent-level Update always
// wholesale-replaces its child rows here rather than diffing them, since
// each editor resubmits its whole child set on every save. insert is called
// once per item in items, in order, receiving that item's index (most
// callers store position/step_order/step_order-within-phase from it).
// entityLabel names the child row kind for error messages (e.g. "on-call
// participant") -- singular, gets pluralized with a trailing "s" for the
// clear-step error.
func replaceChildRows[T any](ctx context.Context, tx pgx.Tx, entityLabel, deleteSQL string, deleteArg any, items []T, insert func(item T, index int) error) error {
	if _, err := tx.Exec(ctx, deleteSQL, deleteArg); err != nil {
		return fmt.Errorf("clear %ss: %w", entityLabel, err)
	}
	for i, item := range items {
		if err := insert(item, i); err != nil {
			return fmt.Errorf("insert %s: %w", entityLabel, err)
		}
	}
	return nil
}
