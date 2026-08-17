// Package telemetry sets up distributed tracing (OpenTelemetry) for each
// cmd/* binary. Instrumentation-only: this package never bundles or starts a
// collector, it only optionally points at one. When no endpoint is
// configured, Setup wires the SDK's own no-op tracer provider, so every span
// start/end elsewhere in the codebase (httpserver/middleware/tracing.go,
// db/tracing.go, llmclient/mcpclient) is a genuinely free no-op rather than a
// silently-buffered-but-never-exported one.
package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup configures the global OTel tracer provider for serviceName ("argusops-api",
// "argusops-worker", or "argusops-ingest" -- see each cmd/*/main.go). When
// otlpEndpoint is empty, it registers the SDK's no-op provider (zero
// goroutines, zero allocation overhead beyond an interface call) and returns
// a shutdown that's safe to call but does nothing. When otlpEndpoint is set,
// it builds a real batch-exporting TracerProvider over OTLP/HTTP and
// registers it as the global provider, so shutdown must be deferred by the
// caller to flush any spans still buffered at process exit.
func Setup(ctx context.Context, serviceName, otlpEndpoint string) (shutdown func(context.Context) error, tracer trace.Tracer, err error) {
	if otlpEndpoint == "" {
		provider := noop.NewTracerProvider()
		otel.SetTracerProvider(provider)
		return func(context.Context) error { return nil }, provider.Tracer(serviceName), nil
	}

	exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(otlpEndpoint))
	if err != nil {
		return nil, nil, fmt.Errorf("create otlp exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(serviceName),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("build otel resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)

	return provider.Shutdown, provider.Tracer(serviceName), nil
}
