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
