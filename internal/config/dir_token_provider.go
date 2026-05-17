package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DirTokenProvider reads session-token and csrf-token from individual
// files in a directory on every Tokens() call. It exists for the
// Kubernetes deployment shape, where the cookies live in a Secret
// mounted as files: the kubelet refreshes those files within ~60s of
// a Secret update, and we re-read on every upstream request, so
// `kubectl create secret ... | apply` is the entire rotation flow —
// no `kubectl rollout restart deployment` needed.
//
// No mtime cache. The files live on tmpfs (kubelet mounts Secrets as
// tmpfs by default), reads are cheap, and we make at most a handful
// of requests per poll interval. Avoiding the cache also dodges a
// subtle edge case: K8s Secret mounts swap a symlink atomically,
// which can confuse mtime-based caches in some kubelet versions.
//
// Implements client.TokenProvider structurally.
type DirTokenProvider struct {
	dir string
}

// NewDirTokenProvider constructs a provider rooted at dir. The
// directory must contain two readable files: session-token and
// csrf-token.
func NewDirTokenProvider(dir string) *DirTokenProvider {
	return &DirTokenProvider{dir: dir}
}

// Dir returns the configured directory (useful for log fields).
func (p *DirTokenProvider) Dir() string { return p.dir }

// Tokens implements client.TokenProvider.
func (p *DirTokenProvider) Tokens(_ context.Context) (string, string, error) {
	sess, err := readTokenFile(filepath.Join(p.dir, "session-token"))
	if err != nil {
		return "", "", err
	}
	csrf, err := readTokenFile(filepath.Join(p.dir, "csrf-token"))
	if err != nil {
		return "", "", err
	}
	return sess, csrf, nil
}

func readTokenFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	val := strings.TrimSpace(string(data))
	if val == "" {
		return "", errors.New(path + " is empty")
	}
	return val, nil
}
