// Package config loads gafferstape's runtime configuration from YAML,
// applies env-var overrides for secrets, and validates the result.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	EnvSessionToken = "GAFFERSTAPE_SESSION_TOKEN"
	EnvCSRFToken    = "GAFFERSTAPE_CSRF_TOKEN"
)

// Config is the full daemon configuration.
type Config struct {
	Listen       string   `yaml:"listen"`
	PollInterval Duration `yaml:"poll_interval"`
	LogLevel     string   `yaml:"log_level"`
	SessionToken string   `yaml:"session_token"`
	CSRFToken    string   `yaml:"csrf_token"`
	Properties   []string `yaml:"properties"`
}

// Default returns a Config populated with the values used when fields
// are omitted from the YAML file.
func Default() Config {
	return Config{
		Listen:       ":9876",
		PollInterval: Duration(15 * time.Minute),
		LogLevel:     "info",
	}
}

// Load reads path, overlays env-var overrides for secrets, and validates.
func Load(path string) (Config, error) {
	cfg := Default()

	if path == "" {
		return Config{}, errors.New("config path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}

	if v := os.Getenv(EnvSessionToken); v != "" {
		cfg.SessionToken = v
	}
	if v := os.Getenv(EnvCSRFToken); v != "" {
		cfg.CSRFToken = v
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate checks the fields that must be set or well-formed at startup.
// Cookie fields are intentionally not required here — the poller (issue
// #3) enforces them when it actually needs them, so the example config
// can boot cleanly for the /healthz acceptance test.
func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen must not be empty")
	}
	if c.PollInterval.Std() <= 0 {
		return fmt.Errorf("poll_interval must be positive, got %s", c.PollInterval.Std())
	}
	if _, err := parseLevel(c.LogLevel); err != nil {
		return err
	}
	return nil
}

// SlogLevel returns the slog.Level matching the configured LogLevel.
// Callers should call Validate first; on an unknown level this returns
// slog.LevelInfo.
func (c *Config) SlogLevel() slog.Level {
	lvl, err := parseLevel(c.LogLevel)
	if err != nil {
		return slog.LevelInfo
	}
	return lvl
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log_level: %q (want debug|info|warn|error)", s)
	}
}

// Duration wraps time.Duration so YAML strings like "15m" parse correctly.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"15m\": %w", err)
	}
	td, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(td)
	return nil
}

// Std returns the wrapped time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }
