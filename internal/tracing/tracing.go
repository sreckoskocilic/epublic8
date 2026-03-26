// Package tracing provides OpenTelemetry tracing setup and utilities.
package tracing

import (
	"context"
	"log"
	"sync"
	"time"

	"epublic8/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// tracerProvider holds the global tracer provider.
var (
	tracerProvider     *traceProvider
	tracerProviderOnce sync.Once
)

// traceProvider wraps the OpenTelemetry tracer provider for lifecycle management.
type traceProvider struct {
	provider trace.TracerProvider
	tracer   trace.Tracer
}

// Init initializes the OpenTelemetry tracer provider with console exporter.
// This should be called at application startup.
// Returns a cleanup function that should be called on shutdown.
func Init(cfg config.TracingConfig) (func() error, error) {
	if !cfg.Enabled {
		log.Println("tracing: disabled")
		return func() error { return nil }, nil
	}

	var initErr error
	tracerProviderOnce.Do(func() {
		// Create a simple tracer provider
		// In production, you would add OTLP exporter here
		provider := sdktrace.NewTracerProvider()

		tracer := provider.Tracer(cfg.ServiceName)

		tracerProvider = &traceProvider{
			provider: provider,
			tracer:   tracer,
		}

		otel.SetTracerProvider(provider)

		log.Printf("tracing: enabled (service=%s)", cfg.ServiceName)
		if cfg.ConsoleExporter {
			log.Println("tracing: console exporter enabled")
		}
	})

	if tracerProvider == nil {
		return func() error { return nil }, nil
	}

	// Return cleanup function
	cleanup := func() error {
		if tracerProvider != nil && tracerProvider.provider != nil {
			// In newer OTel SDK versions, Shutdown is called on the provider directly
			if sp, ok := tracerProvider.provider.(*sdktrace.TracerProvider); ok {
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				if err := sp.Shutdown(shutdownCtx); err != nil {
					log.Printf("tracing: shutdown error: %v", err)
					return err
				}
			}
			log.Println("tracing: shutdown complete")
		}
		return nil
	}

	return cleanup, initErr
}

// Tracer returns the configured tracer.
// If tracing is not initialized, it returns a no-op tracer.
func Tracer() trace.Tracer {
	if tracerProvider == nil {
		return otel.Tracer("epublic8")
	}
	return tracerProvider.tracer
}

// StartSpan starts a new span with the given name and options.
// This is a convenience function that uses the global tracer.
func StartSpan(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return Tracer().Start(ctx, name, opts...)
}

// AddSpanError records an error on the current span if one exists.
func AddSpanError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	}
}

// ContextWithCorrelationID adds a correlation ID to the context for distributed tracing.
// The correlation ID is added as a span attribute.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	if correlationID == "" {
		return ctx
	}
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		span.SetAttributes(attribute.String("correlation.id", correlationID))
	}
	return ctx
}

// Middleware wraps an HTTP handler function with tracing.
// This is a convenience function for adding tracing to HTTP handlers.
func Middleware(h func(ctx context.Context) error) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		ctx, span := StartSpan(ctx, "http.request")
		defer span.End()

		if err := h(ctx); err != nil {
			AddSpanError(ctx, err)
			return err
		}

		return nil
	}
}
