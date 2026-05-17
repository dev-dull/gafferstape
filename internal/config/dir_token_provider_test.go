package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTokenFiles(t *testing.T, dir, sess, csrf string) {
	t.Helper()
	if sess != "" {
		if err := os.WriteFile(filepath.Join(dir, "session-token"), []byte(sess), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if csrf != "" {
		if err := os.WriteFile(filepath.Join(dir, "csrf-token"), []byte(csrf), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirTokenProviderHappyPath(t *testing.T) {
	dir := t.TempDir()
	writeTokenFiles(t, dir, "session-A", "csrf-A")

	p := NewDirTokenProvider(dir)
	sess, csrf, err := p.Tokens(context.Background())
	if err != nil {
		t.Fatalf("Tokens: %v", err)
	}
	if sess != "session-A" || csrf != "csrf-A" {
		t.Errorf("got (%q, %q), want (session-A, csrf-A)", sess, csrf)
	}
}

func TestDirTokenProviderPicksUpFileChange(t *testing.T) {
	dir := t.TempDir()
	writeTokenFiles(t, dir, "old-session", "old-csrf")
	p := NewDirTokenProvider(dir)

	if sess, _, _ := p.Tokens(context.Background()); sess != "old-session" {
		t.Fatalf("initial sess = %q", sess)
	}

	// Simulate the kubelet swapping the file when the Secret updates.
	writeTokenFiles(t, dir, "new-session", "new-csrf")

	sess, csrf, err := p.Tokens(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sess != "new-session" || csrf != "new-csrf" {
		t.Errorf("after edit: got (%q, %q), want (new-session, new-csrf)", sess, csrf)
	}
}

func TestDirTokenProviderTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	// kubelet-written Secret files don't have trailing newlines, but
	// hand-edited / non-K8s usage often does.
	writeTokenFiles(t, dir, "session\n", "csrf  \n\n")

	p := NewDirTokenProvider(dir)
	sess, csrf, _ := p.Tokens(context.Background())
	if sess != "session" || csrf != "csrf" {
		t.Errorf("got (%q, %q), want whitespace-trimmed", sess, csrf)
	}
}

func TestDirTokenProviderMissingSessionFile(t *testing.T) {
	dir := t.TempDir()
	writeTokenFiles(t, dir, "", "csrf-only")
	p := NewDirTokenProvider(dir)

	_, _, err := p.Tokens(context.Background())
	if err == nil {
		t.Fatal("expected error for missing session-token")
	}
	if !strings.Contains(err.Error(), "session-token") {
		t.Errorf("err = %v, want it to mention session-token", err)
	}
}

func TestDirTokenProviderMissingCSRFFile(t *testing.T) {
	dir := t.TempDir()
	writeTokenFiles(t, dir, "session-only", "")
	p := NewDirTokenProvider(dir)

	_, _, err := p.Tokens(context.Background())
	if err == nil {
		t.Fatal("expected error for missing csrf-token")
	}
	if !strings.Contains(err.Error(), "csrf-token") {
		t.Errorf("err = %v, want it to mention csrf-token", err)
	}
}

func TestDirTokenProviderEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeTokenFiles(t, dir, "", "csrf-A")
	// Create empty session-token explicitly:
	if err := os.WriteFile(filepath.Join(dir, "session-token"), []byte("   \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewDirTokenProvider(dir)
	_, _, err := p.Tokens(context.Background())
	if err == nil {
		t.Fatal("expected error for empty session-token")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("err = %v, want it to mention 'empty'", err)
	}
}

func TestDirTokenProviderMissingDir(t *testing.T) {
	p := NewDirTokenProvider("/nonexistent/tokens/dir")
	_, _, err := p.Tokens(context.Background())
	if err == nil {
		t.Fatal("expected error for missing dir")
	}
}

// Sanity: even if the underlying file's mtime doesn't change but the
// content does (theoretically possible on weird filesystems), we still
// pick up the new content because we don't cache.
func TestDirTokenProviderNoStaleCache(t *testing.T) {
	dir := t.TempDir()
	writeTokenFiles(t, dir, "v1", "csrf-A")
	p := NewDirTokenProvider(dir)

	if sess, _, _ := p.Tokens(context.Background()); sess != "v1" {
		t.Fatalf("v1 read failed: %q", sess)
	}

	// Force-write without bumping mtime.
	stat, _ := os.Stat(filepath.Join(dir, "session-token"))
	_ = os.WriteFile(filepath.Join(dir, "session-token"), []byte("v2"), 0o600)
	_ = os.Chtimes(filepath.Join(dir, "session-token"), stat.ModTime(), stat.ModTime())

	if sess, _, _ := p.Tokens(context.Background()); sess != "v2" {
		t.Errorf("v2 read failed: %q (mtime cache leaked through?)", sess)
	}
}
