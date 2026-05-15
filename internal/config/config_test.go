package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTemp(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	path := writeTemp(t, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":9876" {
		t.Errorf("Listen = %q, want :9876", cfg.Listen)
	}
	if cfg.PollInterval.Std() != 15*time.Minute {
		t.Errorf("PollInterval = %s, want 15m", cfg.PollInterval.Std())
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", cfg.LogLevel)
	}
}

func TestLoadFromFile(t *testing.T) {
	path := writeTemp(t, `
listen: ":8080"
poll_interval: 5m
log_level: debug
session_token: "from-file"
csrf_token: "csrf-from-file"
properties:
  - "PROP-1"
  - "PROP-2"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != ":8080" {
		t.Errorf("Listen = %q", cfg.Listen)
	}
	if cfg.PollInterval.Std() != 5*time.Minute {
		t.Errorf("PollInterval = %s", cfg.PollInterval.Std())
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q", cfg.LogLevel)
	}
	if cfg.SessionToken != "from-file" {
		t.Errorf("SessionToken = %q", cfg.SessionToken)
	}
	if len(cfg.Properties) != 2 || cfg.Properties[0] != "PROP-1" {
		t.Errorf("Properties = %v", cfg.Properties)
	}
}

func TestEnvOverridesSecrets(t *testing.T) {
	path := writeTemp(t, `
session_token: "from-file"
csrf_token: "csrf-from-file"
`)
	t.Setenv(EnvSessionToken, "from-env")
	t.Setenv(EnvCSRFToken, "csrf-from-env")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SessionToken != "from-env" {
		t.Errorf("SessionToken = %q, want from-env", cfg.SessionToken)
	}
	if cfg.CSRFToken != "csrf-from-env" {
		t.Errorf("CSRFToken = %q, want csrf-from-env", cfg.CSRFToken)
	}
}

func TestEnvDoesNotClobberFileWhenUnset(t *testing.T) {
	path := writeTemp(t, `session_token: "from-file"`)
	t.Setenv(EnvSessionToken, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SessionToken != "from-file" {
		t.Errorf("SessionToken = %q, want from-file", cfg.SessionToken)
	}
}

func TestInvalidLogLevel(t *testing.T) {
	path := writeTemp(t, `log_level: "noisy"`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "log_level") {
		t.Errorf("err = %v, want it to mention log_level", err)
	}
}

func TestInvalidDuration(t *testing.T) {
	path := writeTemp(t, `poll_interval: "forever"`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "duration") {
		t.Errorf("err = %v, want it to mention duration", err)
	}
}

func TestZeroDuration(t *testing.T) {
	path := writeTemp(t, `poll_interval: 0s`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for zero duration")
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEmptyPath(t *testing.T) {
	_, err := Load("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSlogLevel(t *testing.T) {
	cases := map[string]string{
		"debug": "DEBUG",
		"info":  "INFO",
		"warn":  "WARN",
		"error": "ERROR",
	}
	for in, want := range cases {
		c := Config{LogLevel: in}
		if got := c.SlogLevel().String(); got != want {
			t.Errorf("SlogLevel(%q) = %s, want %s", in, got, want)
		}
	}
}
