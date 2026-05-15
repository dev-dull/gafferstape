package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dev-dull/gafferstape/internal/poller"
)

type stateResponse struct {
	OK               bool            `json:"ok"`
	Error            string          `json:"error,omitempty"`
	SessionExpiresAt string          `json:"session_expires_at,omitempty"`
	Properties       []stateProperty `json:"properties"`
}

type stateProperty struct {
	ID           string         `json:"id"`
	Address      string         `json:"address,omitempty"`
	Timezone     string         `json:"timezone,omitempty"`
	TodayKWh     float64        `json:"today_kwh"`
	YesterdayKWh float64        `json:"yesterday_kwh"`
	LatestHour   *stateHour     `json:"latest_hour,omitempty"`
	Inverter     *stateInverter `json:"inverter,omitempty"`
}

type stateHour struct {
	Time string  `json:"time"`
	KWh  float64 `json:"kwh"`
}

type stateInverter struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Serial       string `json:"serial"`
	Active       bool   `json:"active"`
}

func registerStateAPI(mux *http.ServeMux, snap SnapshotProvider, logger *slog.Logger) {
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		s := snap.Snapshot()
		resp := buildStateResponse(s)

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			logger.Error("write /api/state", "err", err)
		}
	})
}

func buildStateResponse(s poller.Snapshot) stateResponse {
	resp := stateResponse{
		OK:         s.OK,
		Properties: []stateProperty{},
	}
	if !s.OK && s.LastError != "" {
		resp.Error = s.LastError
	}
	if !s.SessionExpiresAt.IsZero() {
		resp.SessionExpiresAt = s.SessionExpiresAt.UTC().Format(time.RFC3339)
	}
	for _, p := range s.Properties {
		resp.Properties = append(resp.Properties, propertyToState(p))
	}
	return resp
}

func propertyToState(p poller.PropertySnapshot) stateProperty {
	out := stateProperty{
		ID:           p.ID,
		Address:      joinAddress(p.Street, p.City, p.State),
		Timezone:     p.Timezone,
		TodayKWh:     p.TodayKWh,
		YesterdayKWh: p.YesterdayKWh,
	}
	if !p.LatestHour.Time.IsZero() {
		t := p.LatestHour.Time
		if loc, err := time.LoadLocation(p.Timezone); err == nil && loc != nil {
			t = t.In(loc)
		}
		out.LatestHour = &stateHour{
			Time: t.Format(time.RFC3339),
			KWh:  p.LatestHour.KWh,
		}
	}
	if p.Inverter.SerialNumber != "" {
		out.Inverter = &stateInverter{
			Manufacturer: p.Inverter.Manufacturer,
			Model:        p.Inverter.ModelNumber,
			Serial:       p.Inverter.SerialNumber,
			Active:       p.Inverter.IsActive,
		}
	}
	return out
}

func joinAddress(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, ", ")
}
