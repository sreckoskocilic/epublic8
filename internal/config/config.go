// Package config provides configuration management with YAML file support
// and environment variable fallback.
package config

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration options for the service.
// All fields have sensible defaults and can be overridden via config file
// or environment variables (environment variables take precedence).
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	OCR      OCRConfig      `yaml:"ocr"`
	EPUB     EPUBConfig     `yaml:"epub"`
	Cleanup  CleanupConfig  `yaml:"cleanup"`
	Security SecurityConfig `yaml:"security"`
	Tracing  TracingConfig  `yaml:"tracing"`
	Metrics  MetricsConfig  `yaml:"metrics"`
}

// ServerConfig holds gRPC and HTTP server settings.
type ServerConfig struct {
	GRPCPort string `yaml:"grpcPort" env:"GRPC_PORT"`
	HTTPPort string `yaml:"httpPort" env:"HTTP_PORT"`
}

// OCRConfig holds OCR processing settings.
type OCRConfig struct {
	// Concurrency controls the maximum number of concurrent OCR page workers.
	// Defaults to GOMAXPROCS.
	Concurrency int `yaml:"concurrency" env:"OCR_CONCURRENCY"`

	// Languages is the ordered list of language codes for OCR (Vision and Tesseract).
	// The first language in the list that is supported by the OCR system will be used.
	// Example: ["srp_latn+hrv", "srp_latn", "eng"]
	Languages []string `yaml:"languages" env:"OCR_LANGUAGES"`
}

// EPUBConfig holds EPUB generation settings.
type EPUBConfig struct {
	// ChapterWords sets the maximum words per chapter in the generated EPUB.
	// Defaults to 1500.
	ChapterWords int `yaml:"chapterWords" env:"EPUB_CHAPTER_WORDS"`

	// OutputDir is the directory where generated EPUB files are stored.
	// If empty, EPUBs are not persisted to disk.
	OutputDir string `yaml:"outputDir" env:"OUTPUT_DIR"`
}

// CleanupConfig controls automatic cleanup of old EPUB files.
type CleanupConfig struct {
	// Enabled enables automatic cleanup of old EPUB files.
	// Defaults to false (disabled).
	Enabled bool `yaml:"enabled" env:"EPUB_CLEANUP_ENABLED"`

	// RetentionHours controls how many hours old EPUBs are retained before deletion.
	// Only used when Cleanup is enabled. Defaults to 24.
	RetentionHours int `yaml:"retentionHours" env:"EPUB_RETENTION_HOURS"`

	// IntervalHours controls how often the cleanup job runs.
	// Only used when Cleanup is enabled. Defaults to 1.
	IntervalHours int `yaml:"intervalHours" env:"EPUB_CLEANUP_INTERVAL_HOURS"`
}

// SecurityConfig holds security-related settings.
type SecurityConfig struct {
	// BasicAuth enables basic authentication for HTTP endpoints.
	// Format: "username:password" or "username:hashed_password" ( bcrypt).
	// Leave empty to disable authentication.
	BasicAuth string `yaml:"basicAuth" env:"BASIC_AUTH"`

	// AllowedHosts restricts requests to specific Host headers.
	// Empty allows any host. Useful in production behind a reverse proxy.
	AllowedHosts []string `yaml:"allowedHosts" env:"ALLOWED_HOSTS"`
}

// TracingConfig holds OpenTelemetry tracing configuration.
type TracingConfig struct {
	// Enabled enables OpenTelemetry tracing.
	// Defaults to false (disabled).
	Enabled bool `yaml:"enabled" env:"TRACING_ENABLED"`

	// ServiceName is the name of the service for tracing.
	// Defaults to "epublic8".
	ServiceName string `yaml:"serviceName" env:"TRACING_SERVICE_NAME"`

	// ConsoleExporter enables console output for tracing (useful for development).
	// Defaults to true when enabled.
	ConsoleExporter bool `yaml:"consoleExporter" env:"TRACING_CONSOLE_EXPORTER"`
}

// MetricsConfig holds Prometheus metrics configuration.
type MetricsConfig struct {
	// Enabled enables Prometheus metrics endpoint.
	// Defaults to true (enabled).
	Enabled bool `yaml:"enabled" env:"METRICS_ENABLED"`

	// Path is the URL path for the metrics endpoint.
	// Defaults to "/metrics".
	Path string `yaml:"path" env:"METRICS_PATH"`
}

// Default returns a Config with all sensible default values.
func Default() *Config {
	return &Config{
		Server: ServerConfig{
			GRPCPort: "50051",
			HTTPPort: "8080",
		},
		OCR: OCRConfig{
			Concurrency: max(1, runtime.GOMAXPROCS(0)),
			Languages:   []string{"srp_latn+hrv", "srp_latn", "eng"},
		},
		EPUB: EPUBConfig{
			ChapterWords: 1500,
			OutputDir:    "",
		},
		Cleanup: CleanupConfig{
			Enabled:        false,
			RetentionHours: 24,
			IntervalHours:  1,
		},
		Security: SecurityConfig{
			BasicAuth:    "",
			AllowedHosts: nil,
		},
		Tracing: TracingConfig{
			Enabled:         false,
			ServiceName:     "epublic8",
			ConsoleExporter: true,
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Path:    "/metrics",
		},
	}
}

// Load reads configuration from a YAML file and applies environment variable overrides.
// If configPath is empty, it checks the CONFIG_PATH environment variable.
// If neither is set, default values are returned.
// Environment variables always take precedence over config file values.
func Load(configPath string) (*Config, error) {
	cfg := Default()

	// Determine config file path
	if configPath == "" {
		configPath = os.Getenv("CONFIG_PATH")
	}

	// If no config file specified, return defaults (env vars will be applied below)
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
		}

		// First unmarshal to get base values
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
		}
	}

	// Apply environment variable overrides
	if err := applyEnvOverrides(cfg); err != nil {
		return nil, fmt.Errorf("failed to apply environment overrides: %w", err)
	}

	return cfg, nil
}

// LoadFromFlag works like Load but also parses -config flag from command line.
// The -config flag takes precedence over CONFIG_PATH env var.
func LoadFromFlag() (*Config, error) {
	configPath := flag.String("config", "", "Path to YAML config file (or set CONFIG_PATH env var)")

	// Parse known flags only (don't error on unknown flags)
	flag.Parse()

	// If -config flag was provided, use it; otherwise fall back to Load() logic
	flagConfigPath := ""
	if flag.Parsed() && *configPath != "" {
		flagConfigPath = *configPath
	}

	return Load(flagConfigPath)
}

// applyEnvOverrides applies environment variable overrides to the config.
// Environment variables take precedence over YAML config values.
func applyEnvOverrides(cfg *Config) error {
	// Server config
	if v := os.Getenv("GRPC_PORT"); v != "" {
		cfg.Server.GRPCPort = v
	}
	if v := os.Getenv("HTTP_PORT"); v != "" {
		cfg.Server.HTTPPort = v
	}

	// OCR config
	if v := os.Getenv("OCR_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Printf("warning: invalid value for OCR_CONCURRENCY, using default %d", cfg.OCR.Concurrency)
		} else {
			cfg.OCR.Concurrency = n
		}
	}
	if v := os.Getenv("OCR_LANGUAGES"); v != "" {
		var langs []string
		for _, lang := range strings.Split(v, ",") {
			if lang = strings.TrimSpace(lang); lang != "" {
				langs = append(langs, lang)
			}
		}
		if len(langs) > 0 {
			cfg.OCR.Languages = langs
		}
	}

	// EPUB config
	if v := os.Getenv("EPUB_CHAPTER_WORDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Printf("warning: invalid value for EPUB_CHAPTER_WORDS, using default %d", cfg.EPUB.ChapterWords)
		} else {
			cfg.EPUB.ChapterWords = n
		}
	}
	if v := os.Getenv("OUTPUT_DIR"); v != "" {
		cfg.EPUB.OutputDir = v
	}

	// Cleanup config
	if v := os.Getenv("EPUB_CLEANUP_ENABLED"); v != "" {
		cfg.Cleanup.Enabled = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("EPUB_RETENTION_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Printf("warning: invalid value for EPUB_RETENTION_HOURS, using default %d", cfg.Cleanup.RetentionHours)
		} else {
			cfg.Cleanup.RetentionHours = n
		}
	}
	if v := os.Getenv("EPUB_CLEANUP_INTERVAL_HOURS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			log.Printf("warning: invalid value for EPUB_CLEANUP_INTERVAL_HOURS, using default %d", cfg.Cleanup.IntervalHours)
		} else {
			cfg.Cleanup.IntervalHours = n
		}
	}

	// Security config
	if v := os.Getenv("BASIC_AUTH"); v != "" {
		cfg.Security.BasicAuth = v
	}
	if v := os.Getenv("ALLOWED_HOSTS"); v != "" {
		cfg.Security.AllowedHosts = strings.Split(v, ",")
	}

	// Tracing config
	if v := os.Getenv("TRACING_ENABLED"); v != "" {
		cfg.Tracing.Enabled = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("TRACING_SERVICE_NAME"); v != "" {
		cfg.Tracing.ServiceName = v
	}
	if v := os.Getenv("TRACING_CONSOLE_EXPORTER"); v != "" {
		cfg.Tracing.ConsoleExporter = strings.ToLower(v) == "true" || v == "1"
	}

	// Metrics config
	if v := os.Getenv("METRICS_ENABLED"); v != "" {
		cfg.Metrics.Enabled = strings.ToLower(v) == "true" || v == "1"
	}
	if v := os.Getenv("METRICS_PATH"); v != "" {
		cfg.Metrics.Path = v
	}

	return nil
}

// String returns a YAML representation of the config (without sensitive values).
func (c *Config) String() string {
	// Create a copy for display (hide sensitive data).
	// AllowedHosts is explicitly copied so the display path cannot mutate the original slice.
	displayCfg := *c
	if len(c.Security.AllowedHosts) > 0 {
		displayCfg.Security.AllowedHosts = make([]string, len(c.Security.AllowedHosts))
		copy(displayCfg.Security.AllowedHosts, c.Security.AllowedHosts)
	}
	if displayCfg.Security.BasicAuth != "" {
		displayCfg.Security.BasicAuth = "***"
	}

	data, err := yaml.Marshal(displayCfg)
	if err != nil {
		return fmt.Sprintf("config error: %v", err)
	}
	return string(data)
}
