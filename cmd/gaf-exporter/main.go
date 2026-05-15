// Command gaf-exporter is the gafferstape daemon: it polls my.gaf.energy
// and exposes the data as Prometheus metrics and a JSON snapshot.
//
// At this checkpoint (issue #1) it only serves /healthz; the API client,
// poller, and metrics arrive in subsequent issues.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/dev-dull/gafferstape/internal/config"
	"github.com/dev-dull/gafferstape/internal/server"
)

// version is overridable at build time via -ldflags.
var version = "0.1.0-dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gaf-exporter", flag.ContinueOnError)
	configPath := fs.String("config", "", "path to YAML config file (required)")
	listenOverride := fs.String("listen", "", "override listen address from config (e.g. :9876)")
	if err := fs.Parse(args); err != nil {
		return err
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
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return server.New(cfg.Listen, logger).Run(ctx)
}
