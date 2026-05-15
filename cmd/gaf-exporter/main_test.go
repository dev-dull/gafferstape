package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// freePort returns a TCP address that was free at the moment of the
// call. There's a tiny race window between the close here and the
// server binding to it; in practice this is not flaky enough to
// matter for a single in-process test.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func waitForHealthz(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s never reached /healthz within %s", addr, timeout)
}

// TestRunGracefulShutdown verifies that the run() entry point honours
// context cancellation — which is exactly what SIGTERM does in main()
// via signal.NotifyContext. We don't bother delivering an actual signal
// here because signal handling is the standard library's responsibility
// and the wiring in main() is trivial; what we want to know is that
// once the context cancels, run() returns within the 5-second window
// the architecture doc promises.
func TestRunGracefulShutdown(t *testing.T) {
	addr := freePort(t)
	cfg := writeConfig(t, "listen: \""+addr+"\"\npoll_interval: 1m\n")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run(ctx, []string{"--config", cfg}) }()

	waitForHealthz(t, addr, 2*time.Second)

	start := time.Now()
	cancel()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("run returned %v, want nil after context cancel", err)
		}
		if elapsed > 5*time.Second {
			t.Errorf("shutdown took %s, exceeds the 5s budget", elapsed)
		}
		t.Logf("clean shutdown in %s", elapsed)
	case <-time.After(8 * time.Second):
		t.Fatal("run did not return within 8s of context cancel")
	}
}

func TestRunRejectsMissingConfig(t *testing.T) {
	err := run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when --config is missing")
	}
}

func TestRunRejectsBadConfig(t *testing.T) {
	cfg := writeConfig(t, "poll_interval: not-a-duration\n")
	err := run(context.Background(), []string{"--config", cfg})
	if err == nil {
		t.Fatal("expected error for bad config")
	}
}
