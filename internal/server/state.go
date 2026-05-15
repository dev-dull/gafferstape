package server

import (
	"log/slog"
	"net/http"
)

// registerStateAPI wires /api/state into mux. Implemented in issue #5.
func registerStateAPI(mux *http.ServeMux, snap SnapshotProvider, logger *slog.Logger) {
	_ = mux
	_ = snap
	_ = logger
}
