// Command gaf-exporter is the gafferstape daemon: it polls my.gaf.energy
// and exposes the data as Prometheus metrics and a JSON snapshot.
//
// At this checkpoint (issue #3) the poller runs but the HTTP surface
// only serves /healthz. /metrics and /api/state arrive in #4 and #5.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/dev-dull/gafferstape/internal/client"
	"github.com/dev-dull/gafferstape/internal/config"
	"github.com/dev-dull/gafferstape/internal/poller"
	"github.com/dev-dull/gafferstape/internal/server"
)

// version is overridable at build time via -ldflags.
var version = "0.1.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

// run is the testable entry point. Pass an already-set-up signal context
// from main, or a manually controlled context from tests that want to
// drive shutdown without OS signals.
func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("gaf-exporter", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to YAML config file (required for normal operation)")
	listenOverride := fs.String("listen", "", "override listen address from config (e.g. :9876)")
	healthcheckMode := fs.Bool("healthcheck", false, "GET /healthz against --listen (default :9876) and exit 0/1 — used by the Docker HEALTHCHECK")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *healthcheckMode {
		return healthcheck(*listenOverride)
	}

	if *configPath == "" {
		return fmt.Errorf("--config is required (try --config config.example.yaml)")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *listenOverride != "" {
		cfg.Listen = *listenOverride
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: cfg.SlogLevel(),
	}))
	slog.SetDefault(logger)

	logger.Info("starting",
		"version", version,
		"listen", cfg.Listen,
		"poll_interval", cfg.PollInterval.Std().String(),
		"log_level", cfg.LogLevel,
		"properties_pinned", len(cfg.Properties),
	)

	pol, err := buildPoller(cfg, logger)
	if err != nil {
		return err
	}

	// Pass the poller as the SnapshotProvider when present; nil keeps
	// the server in /healthz-only mode (no /metrics or /api/state).
	var snap server.SnapshotProvider
	if pol != nil {
		snap = pol
	}
	return runServices(ctx, pol, server.New(cfg.Listen, logger, snap), logger)
}

// buildPoller returns a configured poller, or (nil, nil) when cookies are
// missing — in which case the daemon runs in /healthz-only mode and logs
// a clear warning. Returns a real error only on malformed inputs.
func buildPoller(cfg config.Config, logger *slog.Logger) (*poller.Poller, error) {
	if cfg.SessionToken == "" || cfg.CSRFToken == "" {
		logger.Warn("session_token / csrf_token not configured; running in /healthz-only mode (no upstream polling)")
		return nil, nil
	}

	cli, err := client.New(client.Config{
		SessionToken: cfg.SessionToken,
		CSRFToken:    cfg.CSRFToken,
		UserAgent:    "gafferstape/" + version,
		Logger:       logger.With("component", "client"),
	})
	if err != nil {
		return nil, fmt.Errorf("build client: %w", err)
	}
	pol, err := poller.New(poller.Config{
		API:          cli,
		Interval:     cfg.PollInterval.Std(),
		SessionToken: cfg.SessionToken,
		PropertyIDs:  cfg.Properties,
		Logger:       logger.With("component", "poller"),
	})
	if err != nil {
		return nil, fmt.Errorf("build poller: %w", err)
	}
	return pol, nil
}

// runServices runs the poller (if non-nil) and the HTTP server in
// parallel goroutines. Both share a context: when one returns an error
// the other is cancelled too. Clean exits (nil return) are not
// propagated — the poller exiting on AuthError shouldn't kill /healthz.
func runServices(ctx context.Context, pol *poller.Poller, srv *server.Server, logger *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 2)
	var wg sync.WaitGroup

	if pol != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := pol.Run(ctx); err != nil {
				errs <- fmt.Errorf("poller: %w", err)
				cancel()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Run(ctx); err != nil {
			errs <- fmt.Errorf("server: %w", err)
			cancel()
		}
	}()

	wg.Wait()
	close(errs)

	var firstErr error
	for err := range errs {
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		logger.Error("service exited with error", "err", firstErr)
	}
	return firstErr
}
