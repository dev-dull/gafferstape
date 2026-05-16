package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileTokenProviderReadsFromFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), `
session_token: "file-session"
csrf_token: "file-csrf"
`)
	p := NewFileTokenProvider(path, "", "")
	sess, csrf, err := p.Tokens(context.Background())
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if sess != "file-session" {
		t.Errorf("sess = %q", sess)
	}
	if csrf != "file-csrf" {
		t.Errorf("csrf = %q", csrf)
	}
}

func TestFileTokenProviderEnvFallback(t *testing.T) {
	path := writeFile(t, t.TempDir(), "")
	p := NewFileTokenProvider(path, "env-session", "env-csrf")
	sess, csrf, err := p.Tokens(context.Background())
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if sess != "env-session" || csrf != "env-csrf" {
		t.Errorf("got (%q, %q), want env values", sess, csrf)
	}
}

func TestFileTokenProviderFileOverridesEnv(t *testing.T) {
	path := writeFile(t, t.TempDir(), `
session_token: "from-file"
csrf_token: "from-file-csrf"
`)
	p := NewFileTokenProvider(path, "env-session", "env-csrf")
	sess, csrf, _ := p.Tokens(context.Background())
	if sess != "from-file" || csrf != "from-file-csrf" {
		t.Errorf("got (%q, %q), want file values", sess, csrf)
	}
}

func TestFileTokenProviderPicksUpFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, `
session_token: "old"
csrf_token: "old-csrf"
`)
	p := NewFileTokenProvider(path, "", "")

	sess, _, _ := p.Tokens(context.Background())
	if sess != "old" {
		t.Fatalf("initial sess = %q", sess)
	}

	// Rewrite with a touched mtime. On fast filesystems the second
	// write can land in the same wall-clock second as the first; force
	// the new mtime so the provider notices.
	if err := os.WriteFile(path, []byte(`
session_token: "new"
csrf_token: "new-csrf"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}

	sess2, csrf2, err := p.Tokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess2 != "new" || csrf2 != "new-csrf" {
		t.Errorf("after edit got (%q, %q), want new values", sess2, csrf2)
	}
}

func TestFileTokenProviderCachesByMtime(t *testing.T) {
	// Same file, same mtime → two calls should not re-parse. We verify
	// indirectly by stat-counting via a sentinel: rewrite the file
	// contents but leave the mtime alone, then expect the OLD values.
	dir := t.TempDir()
	path := writeFile(t, dir, `
session_token: "first"
csrf_token: "first-csrf"
`)
	p := NewFileTokenProvider(path, "", "")
	sess1, _, _ := p.Tokens(context.Background())
	if sess1 != "first" {
		t.Fatalf("sess1 = %q", sess1)
	}

	stat, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte(`
session_token: "second"
csrf_token: "second-csrf"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Reset mtime to its earlier value so the provider's cache decides "no change".
	if err := os.Chtimes(path, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}

	sess2, _, _ := p.Tokens(context.Background())
	if sess2 != "first" {
		t.Errorf("sess2 = %q, want %q (cached)", sess2, "first")
	}
}

func TestFileTokenProviderMissingFile(t *testing.T) {
	p := NewFileTokenProvider("/nonexistent/config.yaml", "", "")
	_, _, err := p.Tokens(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file with no env fallback")
	}
}

func TestFileTokenProviderMissingFileWithEnvFallback(t *testing.T) {
	p := NewFileTokenProvider("/nonexistent/config.yaml", "env-s", "env-c")
	sess, csrf, err := p.Tokens(context.Background())
	if err != nil {
		t.Fatalf("Tokens: %v, want fallback to env values", err)
	}
	if sess != "env-s" || csrf != "env-c" {
		t.Errorf("got (%q, %q)", sess, csrf)
	}
}

func TestFileTokenProviderEmptyBothFails(t *testing.T) {
	path := writeFile(t, t.TempDir(), "")
	p := NewFileTokenProvider(path, "", "")
	_, _, err := p.Tokens(context.Background())
	if err == nil {
		t.Fatal("expected error when neither file nor env has tokens")
	}
	if !strings.Contains(err.Error(), "session_token") {
		t.Errorf("err = %v, want mention of session_token", err)
	}
}
