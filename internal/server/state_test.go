package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
	"github.com/dev-dull/gafferstape/internal/poller"
)

type fakeSnap struct{ s poller.Snapshot }

func (f *fakeSnap) Snapshot() poller.Snapshot { return f.s }

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func newStateServer(t *testing.T, s poller.Snapshot) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(NewHandler(discardLogger(), &fakeSnap{s: s}))
	t.Cleanup(ts.Close)
	return ts
}

func decodeState(t *testing.T, ts *httptest.Server) (int, http.Header, map[string]any) {
	t.Helper()
	resp, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, resp.Header, body
}

func TestStateHappyPath(t *testing.T) {
	la := mustLoad(t, "America/Los_Angeles")
	exp := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	hour := time.Date(2026, 5, 14, 15, 0, 0, 0, la)
	snap := poller.Snapshot{
		OK:               true,
		SessionExpiresAt: exp,
		Properties: []poller.PropertySnapshot{{
			ID:           "PROP-FIXTURE-1",
			Street:       "1 Example Way",
			City:         "Example City",
			State:        "OR",
			Timezone:     "America/Los_Angeles",
			TodayKWh:     12.4,
			YesterdayKWh: 21.0,
			LatestHour:   poller.HourSample{Time: hour, KWh: 1.31},
			Inverter: client.Inverter{
				Manufacturer: "Delta",
				ModelNumber:  "INVERTER, M6-TL-US, RGM, WIFI/CELL ENABLED",
				SerialNumber: "FIXTURE-SERIAL-1",
				IsActive:     true,
			},
		}},
	}

	ts := newStateServer(t, snap)
	status, hdr, body := decodeState(t, ts)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got := hdr.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := hdr.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if body["ok"] != true {
		t.Errorf("ok = %v, want true", body["ok"])
	}
	if got := body["session_expires_at"]; got != "2026-06-09T12:00:00Z" {
		t.Errorf("session_expires_at = %v", got)
	}
	if _, ok := body["error"]; ok {
		t.Errorf("error key should be absent when ok=true")
	}

	props, ok := body["properties"].([]any)
	if !ok || len(props) != 1 {
		t.Fatalf("properties = %v", body["properties"])
	}
	p := props[0].(map[string]any)
	if p["id"] != "PROP-FIXTURE-1" {
		t.Errorf("id = %v", p["id"])
	}
	if p["address"] != "1 Example Way, Example City, OR" {
		t.Errorf("address = %v", p["address"])
	}
	if p["timezone"] != "America/Los_Angeles" {
		t.Errorf("timezone = %v", p["timezone"])
	}
	if p["today_kwh"].(float64) != 12.4 {
		t.Errorf("today_kwh = %v", p["today_kwh"])
	}
	if p["yesterday_kwh"].(float64) != 21.0 {
		t.Errorf("yesterday_kwh = %v", p["yesterday_kwh"])
	}
	lh := p["latest_hour"].(map[string]any)
	if lh["time"] != "2026-05-14T15:00:00-07:00" {
		t.Errorf("latest_hour.time = %v", lh["time"])
	}
	if lh["kwh"].(float64) != 1.31 {
		t.Errorf("latest_hour.kwh = %v", lh["kwh"])
	}
	inv := p["inverter"].(map[string]any)
	if inv["manufacturer"] != "Delta" {
		t.Errorf("inverter.manufacturer = %v", inv["manufacturer"])
	}
	if inv["model"] != "INVERTER, M6-TL-US, RGM, WIFI/CELL ENABLED" {
		t.Errorf("inverter.model = %v", inv["model"])
	}
	if inv["serial"] != "FIXTURE-SERIAL-1" {
		t.Errorf("inverter.serial = %v", inv["serial"])
	}
	if inv["active"] != true {
		t.Errorf("inverter.active = %v", inv["active"])
	}
}

func TestStateNotOKPreservesProperties(t *testing.T) {
	snap := poller.Snapshot{
		OK:        false,
		LastError: "upstream 503",
		Properties: []poller.PropertySnapshot{{
			ID:       "PROP-1",
			Street:   "1 Main St",
			City:     "Townsville",
			State:    "CA",
			Timezone: "America/Los_Angeles",
			TodayKWh: 5.5,
		}},
	}
	ts := newStateServer(t, snap)
	status, _, body := decodeState(t, ts)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if body["ok"] != false {
		t.Errorf("ok = %v, want false", body["ok"])
	}
	if body["error"] != "upstream 503" {
		t.Errorf("error = %v", body["error"])
	}
	props := body["properties"].([]any)
	if len(props) != 1 {
		t.Fatalf("properties len = %d, want 1", len(props))
	}
	if props[0].(map[string]any)["id"] != "PROP-1" {
		t.Errorf("property id missing in error case")
	}
}

func TestStatePOSTRejected(t *testing.T) {
	ts := newStateServer(t, poller.Snapshot{OK: true})
	resp, err := http.Post(ts.URL+"/api/state", "application/json", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestStateSessionExpiresAtZeroOmitted(t *testing.T) {
	snap := poller.Snapshot{OK: true}
	ts := newStateServer(t, snap)
	_, _, body := decodeState(t, ts)
	if _, ok := body["session_expires_at"]; ok {
		t.Errorf("session_expires_at should be omitted when zero")
	}
}

func TestStateLatestHourZeroOmitted(t *testing.T) {
	snap := poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{{
			ID:       "PROP-1",
			Timezone: "America/Los_Angeles",
			Inverter: client.Inverter{SerialNumber: "S1"},
		}},
	}
	ts := newStateServer(t, snap)
	_, _, body := decodeState(t, ts)
	p := body["properties"].([]any)[0].(map[string]any)
	if _, ok := p["latest_hour"]; ok {
		t.Errorf("latest_hour should be omitted when zero")
	}
}

func TestStateInverterZeroOmitted(t *testing.T) {
	snap := poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{{
			ID:       "PROP-1",
			Timezone: "America/Los_Angeles",
		}},
	}
	ts := newStateServer(t, snap)
	_, _, body := decodeState(t, ts)
	p := body["properties"].([]any)[0].(map[string]any)
	if _, ok := p["inverter"]; ok {
		t.Errorf("inverter should be omitted when SerialNumber empty")
	}
}

func TestStatePropertiesNilEmitsEmptyArray(t *testing.T) {
	snap := poller.Snapshot{OK: true, Properties: nil}
	ts := newStateServer(t, snap)
	resp, err := http.Get(ts.URL + "/api/state")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["properties"]) != "[]" {
		t.Errorf("properties = %s, want []", raw["properties"])
	}
}

func TestStateAddressOmittedWhenAllEmpty(t *testing.T) {
	snap := poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{{
			ID:       "PROP-1",
			Timezone: "America/Los_Angeles",
		}},
	}
	ts := newStateServer(t, snap)
	_, _, body := decodeState(t, ts)
	p := body["properties"].([]any)[0].(map[string]any)
	if _, ok := p["address"]; ok {
		t.Errorf("address should be omitted when all parts empty")
	}
}

func TestStateAddressSkipsEmptyComponents(t *testing.T) {
	snap := poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{{
			ID:       "PROP-1",
			Street:   "1 Example Way",
			State:    "OR",
			Timezone: "America/Los_Angeles",
		}},
	}
	ts := newStateServer(t, snap)
	_, _, body := decodeState(t, ts)
	p := body["properties"].([]any)[0].(map[string]any)
	if p["address"] != "1 Example Way, OR" {
		t.Errorf("address = %v, want %q", p["address"], "1 Example Way, OR")
	}
}
