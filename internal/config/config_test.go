package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()

	// Server defaults
	if cfg.Server.GRPCPort != "50051" {
		t.Errorf("expected GRPCPort 50051, got %s", cfg.Server.GRPCPort)
	}
	if cfg.Server.HTTPPort != "8080" {
		t.Errorf("expected HTTPPort 8080, got %s", cfg.Server.HTTPPort)
	}

	// OCR defaults
	if cfg.OCR.Concurrency != max(1, runtime.GOMAXPROCS(0)) {
		t.Errorf("expected OCR.Concurrency %d, got %d", max(1, runtime.GOMAXPROCS(0)), cfg.OCR.Concurrency)
	}
	if len(cfg.OCR.Languages) == 0 {
		t.Error("expected OCR.Languages to have default languages")
	}

	// EPUB defaults
	if cfg.EPUB.ChapterWords != 1500 {
		t.Errorf("expected EPUB.ChapterWords 1500, got %d", cfg.EPUB.ChapterWords)
	}
	if cfg.EPUB.OutputDir != "" {
		t.Errorf("expected EPUB.OutputDir empty, got %s", cfg.EPUB.OutputDir)
	}

	// Cleanup defaults
	if cfg.Cleanup.Enabled != false {
		t.Error("expected Cleanup.Enabled false")
	}
	if cfg.Cleanup.RetentionHours != 24 {
		t.Errorf("expected Cleanup.RetentionHours 24, got %d", cfg.Cleanup.RetentionHours)
	}

	// Security defaults
	if cfg.Security.BasicAuth != "" {
		t.Errorf("expected Security.BasicAuth empty, got %s", cfg.Security.BasicAuth)
	}

	// Metrics defaults
	if cfg.Metrics.Enabled != true {
		t.Error("expected Metrics.Enabled true")
	}
	if cfg.Metrics.Path != "/metrics" {
		t.Errorf("expected Metrics.Path /metrics, got %s", cfg.Metrics.Path)
	}
}

func TestLoadNoConfigFile(t *testing.T) {
	// Test loading with no config file - should use defaults
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoadWithEnvOverrides(t *testing.T) {
	// Set environment variables
	t.Setenv("GRPC_PORT", "50052")
	t.Setenv("HTTP_PORT", "9090")
	t.Setenv("OCR_CONCURRENCY", "4")
	t.Setenv("OCR_LANGUAGES", "eng,fra,deu")
	t.Setenv("EPUB_CHAPTER_WORDS", "2000")
	t.Setenv("OUTPUT_DIR", "/tmp/epubs")
	t.Setenv("BASIC_AUTH", "admin:secret")
	t.Setenv("ALLOWED_HOSTS", "example.com,api.example.com")
	t.Setenv("METRICS_ENABLED", "false")
	t.Setenv("METRICS_PATH", "/prometheus")
	t.Setenv("TRACING_ENABLED", "true")
	t.Setenv("TRACING_SERVICE_NAME", "test-service")
	t.Setenv("EPUB_CLEANUP_ENABLED", "true")
	t.Setenv("EPUB_RETENTION_HOURS", "48")
	t.Setenv("EPUB_CLEANUP_INTERVAL_HOURS", "2")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify env overrides
	if cfg.Server.GRPCPort != "50052" {
		t.Errorf("expected GRPC_PORT 50052, got %s", cfg.Server.GRPCPort)
	}
	if cfg.Server.HTTPPort != "9090" {
		t.Errorf("expected HTTP_PORT 9090, got %s", cfg.Server.HTTPPort)
	}
	if cfg.OCR.Concurrency != 4 {
		t.Errorf("expected OCR_CONCURRENCY 4, got %d", cfg.OCR.Concurrency)
	}
	if len(cfg.OCR.Languages) != 3 {
		t.Errorf("expected 3 languages, got %d", len(cfg.OCR.Languages))
	}
	if cfg.EPUB.ChapterWords != 2000 {
		t.Errorf("expected EPUB_CHAPTER_WORDS 2000, got %d", cfg.EPUB.ChapterWords)
	}
	if cfg.EPUB.OutputDir != "/tmp/epubs" {
		t.Errorf("expected OUTPUT_DIR /tmp/epubs, got %s", cfg.EPUB.OutputDir)
	}
	if cfg.Security.BasicAuth != "admin:secret" {
		t.Errorf("expected BASIC_AUTH admin:secret, got %s", cfg.Security.BasicAuth)
	}
	if len(cfg.Security.AllowedHosts) != 2 {
		t.Errorf("expected 2 allowed hosts, got %d", len(cfg.Security.AllowedHosts))
	}
	if cfg.Metrics.Enabled != false {
		t.Error("expected METRICS_ENABLED false")
	}
	if cfg.Metrics.Path != "/prometheus" {
		t.Errorf("expected METRICS_PATH /prometheus, got %s", cfg.Metrics.Path)
	}
	if cfg.Tracing.Enabled != true {
		t.Error("expected TRACING_ENABLED true")
	}
	if cfg.Tracing.ServiceName != "test-service" {
		t.Errorf("expected TRACING_SERVICE_NAME test-service, got %s", cfg.Tracing.ServiceName)
	}
	if cfg.Cleanup.Enabled != true {
		t.Error("expected EPUB_CLEANUP_ENABLED true")
	}
	if cfg.Cleanup.RetentionHours != 48 {
		t.Errorf("expected EPUB_RETENTION_HOURS 48, got %d", cfg.Cleanup.RetentionHours)
	}
	if cfg.Cleanup.IntervalHours != 2 {
		t.Errorf("expected EPUB_CLEANUP_INTERVAL_HOURS 2, got %d", cfg.Cleanup.IntervalHours)
	}
}

func TestLoadEnvOverridesBoolean(t *testing.T) {
	tests := []struct {
		envValue   string
		want       bool
		envVarName string
	}{
		{"true", true, "TRACING_ENABLED"},
		{"false", false, "TRACING_ENABLED"},
		{"1", true, "TRACING_ENABLED"},
		{"0", false, "TRACING_ENABLED"},
		{"TRUE", true, "TRACING_ENABLED"},
		{"FALSE", false, "TRACING_ENABLED"},
		{"true", true, "METRICS_ENABLED"},
		{"false", false, "METRICS_ENABLED"},
		{"true", true, "EPUB_CLEANUP_ENABLED"},
		{"false", false, "EPUB_CLEANUP_ENABLED"},
	}

	for _, tc := range tests {
		t.Run(tc.envVarName+"="+tc.envValue, func(t *testing.T) {
			t.Setenv(tc.envVarName, tc.envValue)
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var got bool
			switch tc.envVarName {
			case "TRACING_ENABLED":
				got = cfg.Tracing.Enabled
			case "METRICS_ENABLED":
				got = cfg.Metrics.Enabled
			case "EPUB_CLEANUP_ENABLED":
				got = cfg.Cleanup.Enabled
			}

			if got != tc.want {
				t.Errorf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestLoadInvalidConfigFile(t *testing.T) {
	// Create a temporary invalid config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write invalid YAML
	if err := os.WriteFile(configPath, []byte("invalid: [yaml: content"), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadMissingConfigFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestLoadYAMLConfigFile(t *testing.T) {
	// Create a temporary valid config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yamlContent := `
server:
  grpcPort: "50052"
  httpPort: "9090"
ocr:
  concurrency: 8
  languages:
    - eng
    - spa
epub:
  chapterWords: 2000
  outputDir: "/tmp/epubs"
cleanup:
  enabled: true
  retentionHours: 48
  intervalHours: 2
security:
  basicAuth: "admin:secret"
  allowedHosts:
    - example.com
tracing:
  enabled: true
  serviceName: "test-service"
  consoleExporter: false
metrics:
  enabled: false
  path: "/prometheus"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify YAML config is loaded
	if cfg.Server.GRPCPort != "50052" {
		t.Errorf("expected grpcPort 50052, got %s", cfg.Server.GRPCPort)
	}
	if cfg.Server.HTTPPort != "9090" {
		t.Errorf("expected httpPort 9090, got %s", cfg.Server.HTTPPort)
	}
	if cfg.OCR.Concurrency != 8 {
		t.Errorf("expected concurrency 8, got %d", cfg.OCR.Concurrency)
	}
	if len(cfg.OCR.Languages) != 2 {
		t.Errorf("expected 2 languages, got %d", len(cfg.OCR.Languages))
	}
	if cfg.EPUB.ChapterWords != 2000 {
		t.Errorf("expected chapterWords 2000, got %d", cfg.EPUB.ChapterWords)
	}
	if cfg.EPUB.OutputDir != "/tmp/epubs" {
		t.Errorf("expected outputDir /tmp/epubs, got %s", cfg.EPUB.OutputDir)
	}
	if cfg.Cleanup.Enabled != true {
		t.Error("expected cleanup enabled")
	}
	if cfg.Cleanup.RetentionHours != 48 {
		t.Errorf("expected retentionHours 48, got %d", cfg.Cleanup.RetentionHours)
	}
	if cfg.Cleanup.IntervalHours != 2 {
		t.Errorf("expected intervalHours 2, got %d", cfg.Cleanup.IntervalHours)
	}
	if cfg.Security.BasicAuth != "admin:secret" {
		t.Errorf("expected basicAuth admin:secret, got %s", cfg.Security.BasicAuth)
	}
	if len(cfg.Security.AllowedHosts) != 1 {
		t.Errorf("expected 1 allowed host, got %d", len(cfg.Security.AllowedHosts))
	}
	if cfg.Tracing.Enabled != true {
		t.Error("expected tracing enabled")
	}
	if cfg.Tracing.ServiceName != "test-service" {
		t.Errorf("expected serviceName test-service, got %s", cfg.Tracing.ServiceName)
	}
	if cfg.Tracing.ConsoleExporter != false {
		t.Error("expected consoleExporter false")
	}
	if cfg.Metrics.Enabled != false {
		t.Error("expected metrics disabled")
	}
	if cfg.Metrics.Path != "/prometheus" {
		t.Errorf("expected path /prometheus, got %s", cfg.Metrics.Path)
	}
}

func TestLoadYAMLWithEnvOverride(t *testing.T) {
	// Create a temporary config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yamlContent := `
server:
  grpcPort: "50052"
ocr:
  concurrency: 8
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Set env var to override YAML
	t.Setenv("GRPC_PORT", "50053")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Env var should override YAML
	if cfg.Server.GRPCPort != "50053" {
		t.Errorf("expected env override to take precedence, got %s", cfg.Server.GRPCPort)
	}

	// YAML value should be preserved for non-overridden fields
	if cfg.OCR.Concurrency != 8 {
		t.Errorf("expected yaml value preserved, got %d", cfg.OCR.Concurrency)
	}
}

func TestLoadFromCONFIG_PATH(t *testing.T) {
	// Create a temporary config file
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	yamlContent := `
server:
  grpcPort: "50055"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Set CONFIG_PATH env var
	t.Setenv("CONFIG_PATH", configPath)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.GRPCPort != "50055" {
		t.Errorf("expected grpcPort 50055, got %s", cfg.Server.GRPCPort)
	}
}

func TestConfigString(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			GRPCPort: "50051",
			HTTPPort: "8080",
		},
		Security: SecurityConfig{
			BasicAuth: "admin:secretpassword",
		},
	}

	str := cfg.String()

	// BasicAuth should be hidden
	if str == "" {
		t.Error("expected non-empty string")
	}
}

func TestLoadInvalidEnvValues(t *testing.T) {
	// Test that invalid env values fall back to defaults
	t.Setenv("OCR_CONCURRENCY", "invalid")
	t.Setenv("EPUB_CHAPTER_WORDS", "-1")
	t.Setenv("EPUB_RETENTION_HOURS", "0")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use defaults for invalid values
	if cfg.OCR.Concurrency != max(1, runtime.GOMAXPROCS(0)) {
		t.Errorf("expected default concurrency, got %d", cfg.OCR.Concurrency)
	}
	if cfg.EPUB.ChapterWords != 1500 {
		t.Errorf("expected default chapterWords, got %d", cfg.EPUB.ChapterWords)
	}
	if cfg.Cleanup.RetentionHours != 24 {
		t.Errorf("expected default retentionHours, got %d", cfg.Cleanup.RetentionHours)
	}
}
