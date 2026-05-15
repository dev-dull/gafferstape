package poller

import (
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
)

// Snapshot is a point-in-time view of what the poller has cached. The
// HTTP surface (issues #4 and #5) reads from this — never from upstream
// directly.
type Snapshot struct {
	// OK is true when the most recent tick completed without any error.
	OK bool
	// AuthFailed is true once we've seen a 401/403 from upstream. Once
	// set, the poller stops scheduling further ticks; only restarting
	// the daemon with fresh cookies clears it.
	AuthFailed bool
	// LastError is the most recent tick's error message, or empty.
	LastError string
	// LastPollAt is the timestamp of the most recent poll attempt.
	LastPollAt time.Time
	// LastSuccessAt is the timestamp of the most recent successful poll.
	LastSuccessAt time.Time
	// SessionExpiresAt is parsed from the Session-Token JWT's exp claim.
	// Zero if the token couldn't be parsed.
	SessionExpiresAt time.Time
	// Properties holds per-property data for everything we successfully
	// polled this tick. May be empty after a metadata-refresh failure.
	Properties []PropertySnapshot
	// ScrapeErrors counts every upstream failure (including those that
	// were subsequently retried successfully) since startup, keyed by
	// the internal op name — "get-properties", "get-account-info",
	// "get-production-hourly", "get-production-daily". The metrics
	// collector emits this as gaf_scrape_errors_total{endpoint=...}.
	ScrapeErrors map[string]int64
}

// PropertySnapshot is the cached data for one property.
type PropertySnapshot struct {
	ID       string
	Street   string
	City     string
	State    string
	Timezone string

	// Inverter is the primary inverter for this property, if known.
	// Multi-inverter setups currently expose only the first one — see
	// the comment in Poller.refreshMetadata.
	Inverter client.Inverter

	// TodayKWh is the sum of today's hourly buckets, in property-local
	// time.
	TodayKWh float64
	// YesterdayKWh is the previous calendar day's total. Stable from
	// the first tick after local midnight until the next.
	YesterdayKWh float64
	// LatestHour is the most recent hourly bucket; zero value if no
	// production has been reported yet today.
	LatestHour HourSample
	// LastSampleAt is the timestamp of the newest bucket we've seen,
	// in the property's local timezone.
	LastSampleAt time.Time
}

// HourSample is one production bucket: KWh produced during the hour
// starting at Time.
type HourSample struct {
	Time time.Time
	KWh  float64
}
