package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
	"github.com/dev-dull/gafferstape/internal/poller"
)

func fetchMetrics(t *testing.T, snap SnapshotProvider) (int, string) {
	t.Helper()
	ts := httptest.NewServer(NewHandler(discardLogger(), snap))
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func mustContain(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, want) {
		t.Errorf("metrics body missing %q\nfull body:\n%s", want, body)
	}
}

func mustNotContain(t *testing.T, body, want string) {
	t.Helper()
	if strings.Contains(body, want) {
		t.Errorf("metrics body unexpectedly contains %q\nfull body:\n%s", want, body)
	}
}

func TestMetricsUpTrue(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{OK: true}}
	status, body := fetchMetrics(t, snap)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	mustContain(t, body, "gaf_up 1")
}

func TestMetricsUpFalse(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{OK: false}}
	_, body := fetchMetrics(t, snap)
	mustContain(t, body, "gaf_up 0")
}

func TestMetricsTwoProperties(t *testing.T) {
	t0 := time.Date(2026, 5, 14, 15, 0, 0, 0, time.UTC)
	snap := &fakeSnap{s: poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{
			{
				ID:           "prop-a",
				Street:       "123 Main St",
				City:         "Springfield",
				State:        "OR",
				TodayKWh:     12.5,
				YesterdayKWh: 21.0,
				LatestHour:   poller.HourSample{Time: t0, KWh: 1.31},
				Inverter: client.Inverter{
					Manufacturer: "Enphase",
					ModelNumber:  "IQ8+",
					SerialNumber: "SN-A-001",
					IsActive:     true,
				},
			},
			{
				ID:           "prop-b",
				Street:       "456 Oak Ave",
				City:         "Eugene",
				State:        "OR",
				TodayKWh:     7.0,
				YesterdayKWh: 9.5,
				LatestHour:   poller.HourSample{Time: t0, KWh: 0.42},
			},
		},
	}}
	_, body := fetchMetrics(t, snap)

	wantTS := strconv.FormatFloat(float64(t0.Unix()), 'g', -1, 64)
	mustContain(t, body, `gaf_last_sample_timestamp_seconds{property_id="prop-a"} `+wantTS)
	mustContain(t, body, `gaf_last_sample_timestamp_seconds{property_id="prop-b"} `+wantTS)

	mustContain(t, body, `gaf_energy_production_kwh_latest_hour{address="123 Main St, Springfield, OR",property_id="prop-a"} 1.31`)
	mustContain(t, body, `gaf_energy_production_kwh_today{address="123 Main St, Springfield, OR",property_id="prop-a"} 12.5`)
	mustContain(t, body, `gaf_energy_production_kwh_yesterday{address="123 Main St, Springfield, OR",property_id="prop-a"} 21`)

	mustContain(t, body, `gaf_energy_production_kwh_latest_hour{address="456 Oak Ave, Eugene, OR",property_id="prop-b"} 0.42`)
	mustContain(t, body, `gaf_energy_production_kwh_today{address="456 Oak Ave, Eugene, OR",property_id="prop-b"} 7`)
	mustContain(t, body, `gaf_energy_production_kwh_yesterday{address="456 Oak Ave, Eugene, OR",property_id="prop-b"} 9.5`)

	mustContain(t, body, `gaf_inverter_info{active="true",manufacturer="Enphase",model="IQ8+",property_id="prop-a",serial="SN-A-001"} 1`)
	mustNotContain(t, body, `property_id="prop-b",serial=`)
}

func TestMetricsSessionExpiresFuture(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		OK:               true,
		SessionExpiresAt: time.Now().Add(time.Hour),
	}}
	_, body := fetchMetrics(t, snap)

	line := findMetricLine(t, body, "gaf_session_expires_seconds ")
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "gaf_session_expires_seconds")), 64)
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	if v < 3598 || v > 3602 {
		t.Errorf("gaf_session_expires_seconds = %v, want ~3600 (±2)", v)
	}
}

func TestMetricsSessionExpiresPast(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		SessionExpiresAt: time.Now().Add(-2 * time.Hour),
	}}
	_, body := fetchMetrics(t, snap)
	line := findMetricLine(t, body, "gaf_session_expires_seconds ")
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "gaf_session_expires_seconds")), 64)
	if err != nil {
		t.Fatalf("parse %q: %v", line, err)
	}
	if v >= 0 {
		t.Errorf("gaf_session_expires_seconds = %v, want negative", v)
	}
}

func TestMetricsSessionExpiresZero(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{}}
	_, body := fetchMetrics(t, snap)
	mustContain(t, body, "gaf_session_expires_seconds 0")
	mustContain(t, body, "gaf_session_expires_at_timestamp_seconds 0")
}

func TestMetricsSessionExpiresAtTimestamp(t *testing.T) {
	exp := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	snap := &fakeSnap{s: poller.Snapshot{
		OK:               true,
		SessionExpiresAt: exp,
	}}
	_, body := fetchMetrics(t, snap)
	want := strconv.FormatFloat(float64(exp.Unix()), 'g', -1, 64)
	mustContain(t, body, "gaf_session_expires_at_timestamp_seconds "+want)
}

func TestMetricsInverterAbsentWhenZero(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{
			{ID: "prop-x", Street: "1 Way", City: "Town", State: "CA"},
		},
	}}
	_, body := fetchMetrics(t, snap)
	mustNotContain(t, body, "gaf_inverter_info")
}

func TestMetricsEmptyPropertiesNoSeries(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{OK: true}}
	_, body := fetchMetrics(t, snap)
	mustNotContain(t, body, "gaf_last_sample_timestamp_seconds")
	mustNotContain(t, body, "gaf_energy_production_kwh_latest_hour")
	mustNotContain(t, body, "gaf_energy_production_kwh_today")
	mustNotContain(t, body, "gaf_energy_production_kwh_yesterday")
	mustNotContain(t, body, "gaf_inverter_info")
}

// Regression for #18: when a property is present but no hourly bucket
// has been observed yet (zero LatestHour.Time), the timestamp metric
// must be ABSENT, not emit time.Time{}.Unix() = -62135596800.
func TestMetricsLastSampleAbsentWhenNoBucket(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{
			{
				ID:       "prop-no-data",
				Street:   "1 Way",
				City:     "Town",
				State:    "CA",
				TodayKWh: 0,
				// LatestHour.Time is the zero value — no buckets observed.
			},
		},
	}}
	_, body := fetchMetrics(t, snap)
	// The corrupt sentinel must not appear anywhere.
	mustNotContain(t, body, "-62135596800")
	// And the metric must be absent for this property (Prometheus
	// "no data" semantics rather than "year 0").
	mustNotContain(t, body, `gaf_last_sample_timestamp_seconds{property_id="prop-no-data"}`)
}

func TestMetricsRejectsPOST(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{OK: true}}
	ts := httptest.NewServer(NewHandler(discardLogger(), snap))
	t.Cleanup(ts.Close)
	resp, err := http.Post(ts.URL+"/metrics", "text/plain", nil)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestMetricsAddressFallbackPartial(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{
			{ID: "prop-c", City: "Portland", TodayKWh: 5},
		},
	}}
	_, body := fetchMetrics(t, snap)
	mustContain(t, body, `gaf_energy_production_kwh_today{address="Portland",property_id="prop-c"} 5`)
}

func TestMetricsAddressFallbackAllEmpty(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		OK: true,
		Properties: []poller.PropertySnapshot{
			{ID: "prop-d", TodayKWh: 3},
		},
	}}
	_, body := fetchMetrics(t, snap)
	mustContain(t, body, `gaf_energy_production_kwh_today{address="unknown",property_id="prop-d"} 3`)
}

func TestMetricsIncludesStandardCollectors(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{OK: true}}
	_, body := fetchMetrics(t, snap)
	mustContain(t, body, "go_goroutines")
	mustContain(t, body, "process_")
}

func TestMetricsScrapeErrors(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{
		OK: true,
		ScrapeErrors: map[string]int64{
			"get-properties":        2,
			"get-production-hourly": 7,
		},
	}}
	_, body := fetchMetrics(t, snap)
	mustContain(t, body, `gaf_scrape_errors_total{endpoint="get-properties"} 2`)
	mustContain(t, body, `gaf_scrape_errors_total{endpoint="get-production-hourly"} 7`)
}

func TestMetricsScrapeErrorsAbsentWhenEmpty(t *testing.T) {
	snap := &fakeSnap{s: poller.Snapshot{OK: true}}
	_, body := fetchMetrics(t, snap)
	// No series should be emitted when nothing has failed yet.
	mustNotContain(t, body, "gaf_scrape_errors_total{")
}

func findMetricLine(t *testing.T, body, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no line with prefix %q in body:\n%s", prefix, body)
	return ""
}
