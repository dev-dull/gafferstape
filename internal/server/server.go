// Package server hosts the HTTP surface of gafferstape: /healthz,
// /metrics (issue #4), and /api/state (issue #5).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/dev-dull/gafferstape/internal/poller"
)

// SnapshotProvider is the read-side interface against the poller. The
// server package depends on this rather than on *poller.Poller so tests
// can substitute a fake provider that returns crafted Snapshots.
type SnapshotProvider interface {
	Snapshot() poller.Snapshot
}

// NewHandler builds the routing tree. Kept separate from the lifecycle
// wrapper so tests can use httptest.NewServer without binding ports.
//
// When snap is nil (e.g. /healthz-only mode with cookies unconfigured),
// /metrics and /api/state are not registered — operators in that mode
// get a clear 404 instead of a confusing empty/error response.
func NewHandler(logger *slog.Logger, snap SnapshotProvider) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz)
	if snap != nil {
		registerMetrics(mux, snap, logger)
		registerStateAPI(mux, snap, logger)
	}
	return mux
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// Server owns the http.Server lifecycle.
type Server struct {
	logger *slog.Logger
	srv    *http.Server
}

func New(addr string, logger *slog.Logger, snap SnapshotProvider) *Server {
	return &Server{
		logger: logger,
		srv: &http.Server{
			Addr:              addr,
			Handler:           NewHandler(logger, snap),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Run serves until ctx is cancelled, then shuts down gracefully with a
// 5-second deadline. Returns nil on a clean shutdown.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("listening", "addr", s.srv.Addr)
		errCh <- s.srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		<-errCh
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
