package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// FileTokenProvider satisfies client.TokenProvider by re-reading the
// session_token and csrf_token fields out of a YAML file on each call,
// caching by mtime so it only re-parses when the file actually
// changed. The user can edit config.yaml in place and the daemon
// picks up the new cookies on its next upstream request without
// restarting the container.
//
// Env-var values captured at construction time act as a fallback when
// the file's token fields are empty — this preserves the
// docker-compose + .env workflow for first-time deployments (where
// the user hasn't necessarily put tokens in config.yaml).
//
// Implements client.TokenProvider structurally (no import cycle).
type FileTokenProvider struct {
	path       string
	envSession string
	envCSRF    string

	mu      sync.Mutex
	modTime time.Time
	session string
	csrf    string
}

// NewFileTokenProvider constructs a provider rooted at the same
// config.yaml the daemon was started with. envSession and envCSRF are
// the env-var values read at startup; pass empty strings if there are
// no env-var fallbacks.
func NewFileTokenProvider(path, envSession, envCSRF string) *FileTokenProvider {
	return &FileTokenProvider{
		path:       path,
		envSession: envSession,
		envCSRF:    envCSRF,
	}
}

// Tokens implements client.TokenProvider. Returns the freshest tokens
// from the file (or the env-var fallback) and an error only when no
// usable pair can be resolved.
func (p *FileTokenProvider) Tokens(_ context.Context) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	stat, err := os.Stat(p.path)
	if err != nil {
		// File gone or unreadable — keep going with env-var fallback if we have one.
		if p.envSession != "" && p.envCSRF != "" {
			return p.envSession, p.envCSRF, nil
		}
		return "", "", fmt.Errorf("FileTokenProvider stat %s: %w", p.path, err)
	}

	if stat.ModTime().Equal(p.modTime) && p.session != "" && p.csrf != "" {
		return p.session, p.csrf, nil
	}

	data, err := os.ReadFile(p.path)
	if err != nil {
		return "", "", fmt.Errorf("FileTokenProvider read %s: %w", p.path, err)
	}
	var parsed struct {
		SessionToken string `yaml:"session_token"`
		CSRFToken    string `yaml:"csrf_token"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return "", "", fmt.Errorf("FileTokenProvider parse %s: %w", p.path, err)
	}

	sess := parsed.SessionToken
	if sess == "" {
		sess = p.envSession
	}
	csrf := parsed.CSRFToken
	if csrf == "" {
		csrf = p.envCSRF
	}
	if sess == "" || csrf == "" {
		return "", "", errors.New("FileTokenProvider: session_token / csrf_token not set in config file or env vars")
	}

	p.session = sess
	p.csrf = csrf
	p.modTime = stat.ModTime()
	return sess, csrf, nil
}
