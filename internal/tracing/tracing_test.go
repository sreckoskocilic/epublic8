package tracing

import (
	"context"
	"errors"
	"sync"
	"testing"

	"epublic8/internal/config"
)

// resetState resets package-level tracing globals between tests so that
// Init can be called multiple times within the same test binary run.
func resetState(t *testing.T) {
	t.Helper()
	tracerProvider = nil
	tracerProviderOnce = sync.Once{}
}

func TestInitDisabled(t *testing.T) {
	resetState(t)
	cfg := config.TracingConfig{Enabled: false}
	cleanup, err := Init(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup function")
	}
	// Cleanup should not panic when tracing is disabled
	if err := cleanup(); err != nil {
		t.Errorf("cleanup returned error: %v", err)
	}
}

func TestInitEnabled(t *testing.T) {
	resetState(t)
	cfg := config.TracingConfig{
		Enabled:         true,
		ServiceName:     "test-service",
		ConsoleExporter: false,
	}
	cleanup, err := Init(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cleanup == nil {
		t.Fatal("expected non-nil cleanup function")
	}
	if err := cleanup(); err != nil {
		t.Errorf("cleanup returned error: %v", err)
	}
}

func TestTracer(t *testing.T) {
	tracer := Tracer()
	if tracer == nil {
		t.Error("expected non-nil tracer")
	}
}

func TestStartSpan(t *testing.T) {
	ctx := context.Background()
	_, span := StartSpan(ctx, "test-span")
	if span == nil {
		t.Fatal("expected non-nil span")
	}
	span.End()

	// Span should be valid
	if !span.SpanContext().IsValid() {
		// No-op tracer may return invalid span, which is acceptable
		t.Log("span context is invalid (no-op tracer)")
	}
}

func TestAddSpanError(t *testing.T) {
	ctx := context.Background()

	t.Run("nil error does not panic", func(t *testing.T) {
		AddSpanError(ctx, nil)
	})

	t.Run("error on context without span does not panic", func(t *testing.T) {
		AddSpanError(ctx, errors.New("test error"))
	})

	t.Run("error on context with span", func(t *testing.T) {
		ctx, span := StartSpan(ctx, "test-span")
		defer span.End()
		AddSpanError(ctx, errors.New("test error"))
	})
}

func TestContextWithCorrelationID(t *testing.T) {
	ctx := context.Background()

	t.Run("empty ID returns same context", func(t *testing.T) {
		result := ContextWithCorrelationID(ctx, "")
		if result != ctx {
			t.Error("expected same context for empty correlation ID")
		}
	})

	t.Run("non-empty ID returns context", func(t *testing.T) {
		result := ContextWithCorrelationID(ctx, "req-123")
		if result == nil {
			t.Error("expected non-nil context")
		}
	})
}
