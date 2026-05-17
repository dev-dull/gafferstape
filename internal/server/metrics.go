package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func registerMetrics(mux *http.ServeMux, snap SnapshotProvider, logger *slog.Logger) {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		&gafCollector{snap: snap},
	)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog: slogPrintln{logger: logger},
		Registry: reg,
	}))
}

type slogPrintln struct{ logger *slog.Logger }

func (s slogPrintln) Println(v ...interface{}) {
	s.logger.Error("metrics handler", "msg", v)
}

type gafCollector struct {
	snap SnapshotProvider
}

var (
	descUp = prometheus.NewDesc(
		"gaf_up",
		"1 if the most recent poll succeeded, 0 otherwise.",
		nil, nil,
	)
	descSessionExpires = prometheus.NewDesc(
		"gaf_session_expires_seconds",
		"Seconds until the Session-Token JWT expires. Negative if already expired; 0 if unknown.",
		nil, nil,
	)
	descSessionExpiresAt = prometheus.NewDesc(
		"gaf_session_expires_at_timestamp_seconds",
		"Unix timestamp (UTC) of the Session-Token JWT's exp claim. 0 if unknown.",
		nil, nil,
	)
	descLastSample = prometheus.NewDesc(
		"gaf_last_sample_timestamp_seconds",
		"Unix time of the newest hourly bucket seen for this property.",
		[]string{"property_id"}, nil,
	)
	descLatestHour = prometheus.NewDesc(
		"gaf_energy_production_kwh_latest_hour",
		"kWh produced in the most recent complete hourly bucket.",
		[]string{"property_id", "address"}, nil,
	)
	descToday = prometheus.NewDesc(
		"gaf_energy_production_kwh_today",
		"Sum of today's hourly production buckets in kWh.",
		[]string{"property_id", "address"}, nil,
	)
	descYesterday = prometheus.NewDesc(
		"gaf_energy_production_kwh_yesterday",
		"Previous calendar day's total production in kWh.",
		[]string{"property_id", "address"}, nil,
	)
	descInverter = prometheus.NewDesc(
		"gaf_inverter_info",
		"Inverter hardware metadata; value is always 1.",
		[]string{"property_id", "manufacturer", "model", "serial", "active"}, nil,
	)
	descScrapeErrors = prometheus.NewDesc(
		"gaf_scrape_errors_total",
		"Cumulative count of upstream call failures since startup, including those subsequently retried successfully.",
		[]string{"endpoint"}, nil,
	)
)

func (c *gafCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- descUp
	ch <- descSessionExpires
	ch <- descSessionExpiresAt
	ch <- descLastSample
	ch <- descLatestHour
	ch <- descToday
	ch <- descYesterday
	ch <- descInverter
	ch <- descScrapeErrors
}

func (c *gafCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.snap.Snapshot()

	up := 0.0
	if s.OK {
		up = 1.0
	}
	ch <- prometheus.MustNewConstMetric(descUp, prometheus.GaugeValue, up)
	ch <- prometheus.MustNewConstMetric(descSessionExpires, prometheus.GaugeValue, sessionExpiresSeconds(s.SessionExpiresAt))
	ch <- prometheus.MustNewConstMetric(descSessionExpiresAt, prometheus.GaugeValue, sessionExpiresAtUnix(s.SessionExpiresAt))

	for _, p := range s.Properties {
		addr := formatAddress(p.Street, p.City, p.State)
		// Skip the metric entirely when we have no observed sample yet.
		// Emitting time.Time{}.Unix() yields -62135596800 (Jan 1 year 1),
		// which corrupts every Grafana panel doing `time() - X` against
		// it. Absent semantics matches the gaf_inverter_info pattern.
		if !p.LatestHour.Time.IsZero() {
			ch <- prometheus.MustNewConstMetric(descLastSample, prometheus.GaugeValue, float64(p.LatestHour.Time.Unix()), p.ID)
		}
		ch <- prometheus.MustNewConstMetric(descLatestHour, prometheus.GaugeValue, p.LatestHour.KWh, p.ID, addr)
		ch <- prometheus.MustNewConstMetric(descToday, prometheus.GaugeValue, p.TodayKWh, p.ID, addr)
		ch <- prometheus.MustNewConstMetric(descYesterday, prometheus.GaugeValue, p.YesterdayKWh, p.ID, addr)

		if p.Inverter != (client.Inverter{}) {
			ch <- prometheus.MustNewConstMetric(
				descInverter,
				prometheus.GaugeValue,
				1,
				p.ID,
				p.Inverter.Manufacturer,
				p.Inverter.ModelNumber,
				p.Inverter.SerialNumber,
				strconv.FormatBool(p.Inverter.IsActive),
			)
		}
	}

	for endpoint, count := range s.ScrapeErrors {
		ch <- prometheus.MustNewConstMetric(descScrapeErrors, prometheus.CounterValue, float64(count), endpoint)
	}
}

func sessionExpiresSeconds(exp time.Time) float64 {
	if exp.IsZero() {
		return 0
	}
	return time.Until(exp).Seconds()
}

func sessionExpiresAtUnix(exp time.Time) float64 {
	if exp.IsZero() {
		return 0
	}
	return float64(exp.Unix())
}

func formatAddress(street, city, state string) string {
	parts := make([]string, 0, 3)
	for _, p := range []string{street, city, state} {
		if p = strings.TrimSpace(p); p != "" {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, ", ")
}
