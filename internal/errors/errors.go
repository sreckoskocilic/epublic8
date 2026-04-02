// Package errors provides structured error types and utilities for consistent
// error handling across the epublic8 service.
package errors

import (
	"errors"
	"fmt"
	"log"
)

// Level represents the severity level for error logging.
type Level int

const (
	// LevelDebug is for verbose debugging information.
	LevelDebug Level = iota
	// LevelInfo is for general informational messages.
	LevelInfo
	// LevelWarn is for warning conditions that don't prevent operation.
	LevelWarn
	// LevelError is for error conditions that prevent specific operations.
	LevelError
)

// String returns a string representation of the level.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger defines the interface for structured logging.
type Logger interface {
	Log(level Level, msg string, args ...any)
}

// DefaultLogger is the default logger implementation that writes to log.Printf.
type DefaultLogger struct{}

// Log writes a log message at the specified level.
func (d *DefaultLogger) Log(level Level, msg string, args ...any) {
	log.Printf("[%s] %s", level.String(), fmt.Sprintf(msg, args...))
}

// logger is the package-level logger instance.
var logger Logger = &DefaultLogger{}

// SetLogger sets the package-level logger for error logging.
func SetLogger(l Logger) {
	logger = l
}

// Log logs a message at the specified level using the package logger.
func Log(level Level, msg string, args ...any) {
	logger.Log(level, msg, args...)
}

// LogError logs an error with context at ERROR level.
func LogError(err error, msg string, args ...any) {
	if err != nil {
		logger.Log(LevelError, msg+": %v", append(args, err)...)
	} else {
		logger.Log(LevelError, msg, args...)
	}
}

// LogWarn logs a warning message at WARN level.
func LogWarn(msg string, args ...any) {
	logger.Log(LevelWarn, msg, args...)
}

// LogInfo logs an informational message at INFO level.
func LogInfo(msg string, args ...any) {
	logger.Log(LevelInfo, msg, args...)
}

// LogDebug logs a debug message at DEBUG level.
func LogDebug(msg string, args ...any) {
	logger.Log(LevelDebug, msg, args...)
}

// Common error variables for sentinel errors.
var (
	// ErrNotFound is returned when a requested resource is not found.
	ErrNotFound = errors.New("resource not found")

	// ErrInvalidInput is returned when input validation fails.
	ErrInvalidInput = errors.New("invalid input")

	// ErrProcessingFailed is returned when document processing fails.
	ErrProcessingFailed = errors.New("processing failed")

	// ErrTimeout is returned when an operation times out.
	ErrTimeout = errors.New("operation timed out")

	// ErrUnavailable is returned when a required service is unavailable.
	ErrUnavailable = errors.New("service unavailable")
)

// ProcessingError represents an error that occurred during document processing.
type ProcessingError struct {
	Op  string // Operation that failed
	Err error  // Underlying error
}

// Error returns a formatted error message.
func (e *ProcessingError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	}
	return e.Op
}

// Unwrap returns the underlying error for error wrapping.
func (e *ProcessingError) Unwrap() error {
	return e.Err
}

// NewProcessingError creates a new ProcessingError with context.
func NewProcessingError(op string, err error) error {
	return &ProcessingError{Op: op, Err: err}
}

// ConfigError represents a configuration error.
type ConfigError struct {
	Key   string // Configuration key that caused the error
	Value string // The invalid value (may be redacted for secrets)
	Err   error  // Underlying error
}

// Error returns a formatted error message.
func (e *ConfigError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("config %s: %v", e.Key, e.Err)
	}
	return fmt.Sprintf("config %s", e.Key)
}

// Unwrap returns the underlying error.
func (e *ConfigError) Unwrap() error {
	return e.Err
}

// NewConfigError creates a new ConfigError.
func NewConfigError(key string, value string, err error) error {
	return &ConfigError{Key: key, Value: value, Err: err}
}
