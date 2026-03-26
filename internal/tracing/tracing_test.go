package tracing

import (
	"context"
	"errors"
	"testing"

	"epublic8/internal/config"
)

func TestInitDisabled(t *testing.T) {
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

func TestMiddleware(t *testing.T) {
	t.Run("wraps handler without error", func(t *testing.T) {
		handler := func(ctx context.Context) error {
			return nil
		}
		wrapped := Middleware(handler)
		err := wrapped(context.Background())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("wraps handler with error", func(t *testing.T) {
		expected := errors.New("handler error")
		handler := func(ctx context.Context) error {
			return expected
		}
		wrapped := Middleware(handler)
		err := wrapped(context.Background())
		if err != expected {
			t.Errorf("expected %v, got %v", expected, err)
		}
	})
}
