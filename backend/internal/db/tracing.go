package db

import (
	"context"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// dbTracer implements pgx.QueryTracer, starting one span per Query/QueryRow/
// Exec call. Attributes are limited to duration (via the span's own
// start/end) and rows-affected/error -- SQL text and args are deliberately
// never attached to a span, since args routinely carry request parameter
// values that shouldn't end up mirrored into a trace backend.
type dbTracer struct {
	tracer trace.Tracer
}

func (t *dbTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	ctx, _ = t.tracer.Start(ctx, "db.query")
	return ctx
}

func (t *dbTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	defer span.End()
	if data.Err != nil {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
		return
	}
	span.SetAttributes(attribute.Int64("db.rows_affected", data.CommandTag.RowsAffected()))
}
