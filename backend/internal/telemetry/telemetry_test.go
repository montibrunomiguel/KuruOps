package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetup_EmptyEndpointReturnsWorkingNoopTracer(t *testing.T) {
	shutdown, tracer, err := Setup(context.Background(), "kuruops-test", "")
	require.NoError(t, err)
	require.NotNil(t, tracer)

	// Starting and ending a span on the no-op tracer must not panic --
	// that's the whole point of the no-op path (see Setup's doc comment).
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()

	require.NoError(t, shutdown(context.Background()))
}

func TestSetup_InvalidEndpointStillReturnsUsableTracer(t *testing.T) {
	// otlptracehttp.New doesn't dial eagerly -- an unreachable/malformed
	// endpoint is only ever discovered on export, so Setup itself should
	// succeed and hand back a tracer that's safe to start spans on.
	shutdown, tracer, err := Setup(context.Background(), "kuruops-test", "http://127.0.0.1:0")
	require.NoError(t, err)
	require.NotNil(t, tracer)

	_, span := tracer.Start(context.Background(), "test-span")
	span.End()

	require.NoError(t, shutdown(context.Background()))
}
