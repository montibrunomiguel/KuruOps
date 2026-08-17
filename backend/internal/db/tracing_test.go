package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestDBTracer_RecordsSpanPerQuery(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tr := &dbTracer{tracer: provider.Tracer("test")}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "select 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{CommandTag: pgconn.NewCommandTag("SELECT 1")})

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "db.query", spans[0].Name())
	require.NotEqual(t, codes.Error, spans[0].Status().Code)
}

func TestDBTracer_RecordsErrorOnFailedQuery(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tr := &dbTracer{tracer: provider.Tracer("test")}

	ctx := tr.TraceQueryStart(context.Background(), nil, pgx.TraceQueryStartData{SQL: "select 1"})
	tr.TraceQueryEnd(ctx, nil, pgx.TraceQueryEndData{Err: errors.New("boom")})

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "db.query", spans[0].Name())
	require.NotEmpty(t, spans[0].Events(), "expected the error to be recorded as a span event")
	require.Equal(t, codes.Error, spans[0].Status().Code)
}
