package cmd

import (
	"context"
	"fmt"

	"github.com/USA-RedDragon/nexrad-aws-notifier/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const serviceName = "nexrad-aws-notifier"

// setupTracing installs a global tracer provider that exports over OTLP gRPC
// to the configured endpoint, so the spans otelgin records go somewhere. The
// returned function flushes and stops it. With tracing disabled it does
// nothing and returns a no-op.
func setupTracing(ctx context.Context, cfg *config.Config, version string) (func(context.Context) error, error) {
	if !cfg.HTTP.Tracing.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlptrace.New(
		ctx,
		otlptracegrpc.NewClient(
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(cfg.HTTP.Tracing.OTLPEndpoint),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}
	resources, err := resource.New(
		ctx,
		resource.WithAttributes(
			attribute.String("service.name", serviceName),
			attribute.String("service.version", version),
			attribute.String("library.language", "go"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create trace resources: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resources),
	)
	otel.SetTracerProvider(provider)
	// The provider, not the exporter, has to be shut down: it owns the batcher,
	// and stopping only the exporter drops whatever the batcher still holds.
	return provider.Shutdown, nil
}
