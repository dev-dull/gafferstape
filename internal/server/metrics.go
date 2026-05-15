package server

import (
	"log/slog"
	"net/http"
)

// registerMetrics wires /metrics into mux. Implemented in issue #4.
func registerMetrics(mux *http.ServeMux, snap SnapshotProvider, logger *slog.Logger) {
	_ = mux
	_ = snap
	_ = logger
}
