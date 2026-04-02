// Package errors provides structured error types and utilities for consistent
// error handling across the epublic8 service.
package errors

import (
	"errors"
	"testing"
)

func TestErrorLevels(t *testing.T) {
	if LevelDebug.String() != "DEBUG" {
		t.Errorf("LevelDebug.String() = %q, want DEBUG", LevelDebug.String())
	}
	if LevelInfo.String() != "INFO" {
		t.Errorf("LevelInfo.String() = %q, want INFO", LevelInfo.String())
	}
	if LevelWarn.String() != "WARN" {
		t.Errorf("LevelWarn.String() = %q, want WARN", LevelWarn.String())
	}
	if LevelError.String() != "ERROR" {
		t.Errorf("LevelError.String() = %q, want ERROR", LevelError.String())
	}
}

func TestDefaultLogger(t *testing.T) {
	logger := &DefaultLogger{}
	// Should not panic
	logger.Log(LevelInfo, "test message")
	logger.Log(LevelError, "error: %v", errors.New("test"))
}

func TestSetLogger(t *testing.T) {
	original := logger
	defer func() { logger = original }()

	customLogger := &DefaultLogger{}
	SetLogger(customLogger)
	if logger != customLogger {
		t.Error("SetLogger did not update package logger")
	}
}

func TestLogHelpers(t *testing.T) {
	// Should not panic
	Log(LevelInfo, "info message")
	LogError(errors.New("test error"), "error occurred")
	LogWarn("warning message")
	LogDebug("debug message")
}

func TestSentinelErrors(t *testing.T) {
	if ErrNotFound.Error() != "resource not found" {
		t.Errorf("ErrNotFound = %v, want 'resource not found'", ErrNotFound)
	}
	if ErrInvalidInput.Error() != "invalid input" {
		t.Errorf("ErrInvalidInput = %v, want 'invalid input'", ErrInvalidInput)
	}
	if ErrProcessingFailed.Error() != "processing failed" {
		t.Errorf("ErrProcessingFailed = %v, want 'processing failed'", ErrProcessingFailed)
	}
	if ErrTimeout.Error() != "operation timed out" {
		t.Errorf("ErrTimeout = %v, want 'operation timed out'", ErrTimeout)
	}
	if ErrUnavailable.Error() != "service unavailable" {
		t.Errorf("ErrUnavailable = %v, want 'service unavailable'", ErrUnavailable)
	}
}

func TestProcessingError(t *testing.T) {
	underlying := errors.New("underlying error")
	err := &ProcessingError{Op: "test op", Err: underlying}

	if err.Error() != "test op: underlying error" {
		t.Errorf("ProcessingError.Error() = %q, want 'test op: underlying error'", err.Error())
	}
	if err.Unwrap() != underlying {
		t.Error("ProcessingError.Unwrap() did not return underlying error")
	}

	// Test with nil underlying error
	errNil := &ProcessingError{Op: "test op", Err: nil}
	if errNil.Error() != "test op" {
		t.Errorf("ProcessingError with nil Err.Error() = %q, want 'test op'", errNil.Error())
	}
	if errNil.Unwrap() != nil {
		t.Error("ProcessingError.Unwrap() with nil Err should return nil")
	}

	// Test NewProcessingError
	wrapped := NewProcessingError("test op", underlying)
	if pw, ok := wrapped.(*ProcessingError); !ok {
		t.Error("NewProcessingError did not return *ProcessingError")
	} else {
		if pw.Op != "test op" {
			t.Errorf("NewProcessingError.Op = %q, want 'test op'", pw.Op)
		}
		if pw.Err != underlying {
			t.Error("NewProcessingError.Err did not wrap underlying error")
		}
	}
}

func TestConfigError(t *testing.T) {
	underlying := errors.New("invalid value")
	err := &ConfigError{Key: "test_key", Value: "test_value", Err: underlying}

	if err.Error() != "config test_key: invalid value" {
		t.Errorf("ConfigError.Error() = %q, want 'config test_key: invalid value'", err.Error())
	}
	if err.Unwrap() != underlying {
		t.Error("ConfigError.Unwrap() did not return underlying error")
	}

	// Test with nil underlying error
	errNil := &ConfigError{Key: "test_key", Value: "test_value", Err: nil}
	if errNil.Error() != "config test_key" {
		t.Errorf("ConfigError with nil Err.Error() = %q, want 'config test_key'", errNil.Error())
	}
	if errNil.Unwrap() != nil {
		t.Error("ConfigError.Unwrap() with nil Err should return nil")
	}

	// Test NewConfigError
	wrapped := NewConfigError("test_key", "test_value", underlying)
	if cw, ok := wrapped.(*ConfigError); !ok {
		t.Error("NewConfigError did not return *ConfigError")
	} else {
		if cw.Key != "test_key" {
			t.Errorf("NewConfigError.Key = %q, want 'test_key'", cw.Key)
		}
		if cw.Value != "test_value" {
			t.Errorf("NewConfigError.Value = %q, want 'test_value'", cw.Value)
		}
		if cw.Err != underlying {
			t.Error("NewConfigError.Err did not wrap underlying error")
		}
	}
}
