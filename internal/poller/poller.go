// Package poller refreshes data from my.gaf.energy on a fixed interval
// and caches the latest results in memory. The HTTP surface (issues #4
// and #5) reads from the cache; it never blocks on upstream.
//
// The polling loop is split into two halves: Run owns the timer and
// context cancellation, while pollOnce does the actual fetch work given
// a controlled `now`. Tests drive pollOnce directly, which is much more
// honest than faking timers.
package poller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
)

const (
	defaultPollInterval = 15 * time.Minute
	// metadataMaxAge controls how often we re-fetch /property/get-all
	// and /auth/get-account-info. Property/inverter metadata rarely
	// changes, so once a day is plenty.
	metadataMaxAge = 24 * time.Hour
)

// API is the subset of *client.Client the poller depends on. Tests
// substitute a fake.
type API interface {
	GetProperties(ctx context.Context) ([]client.Property, error)
	GetAccountInfo(ctx context.Context) (client.Account, error)
	GetProduction(ctx context.Context, propertyID string, interval client.Interval, start, end time.Time, tz string) (client.Production, error)
}

// Config configures a new Poller.
type Config struct {
	// API is the upstream client. Required.
	API API
	// Interval between ticks. Defaults to 15m.
	Interval time.Duration
	// SessionToken (the Session-Token cookie value, a JWT) is decoded
	// only to extract the exp claim — exposed as SessionExpiresAt so
	// alerting can warn before manual cookie refresh is needed. Required.
	SessionToken string
	// PropertyIDs optionally restricts polling to a subset of the
	// account's properties. Empty = all.
	PropertyIDs []string
	// Logger receives a debug line per tick + warn/error on failures.
	Logger *slog.Logger
	// Now is an injectable clock. Defaults to time.Now. Useful only in
	// tests; production should leave it nil.
	Now func() time.Time
}

// Poller drives periodic refresh of the in-memory snapshot.
type Poller struct {
	api            API
	interval       time.Duration
	propertyFilter map[string]bool // nil = no filter
	logger         *slog.Logger
	now            func() time.Time

	// Loop-local state: only touched from Run/pollOnce, no lock.
	metadataLastRefresh time.Time
	propertyMeta        []client.Property
	inverters           map[string][]client.Inverter
	perProperty         map[string]propertyState
	sessionExpiresAt    time.Time
	authFailed          bool

	mu   sync.RWMutex
	snap Snapshot
}

// propertyState tracks per-property data that survives across ticks but
// isn't exposed in the public Snapshot.
type propertyState struct {
	LastSeenLocalDate string
	YesterdayKWh      float64
}

// New constructs a Poller.
func New(cfg Config) (*Poller, error) {
	if cfg.API == nil {
		return nil, errors.New("poller: API is required")
	}
	if cfg.SessionToken == "" {
		return nil, errors.New("poller: SessionToken is required (for JWT exp parsing)")
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	var filter map[string]bool
	if len(cfg.PropertyIDs) > 0 {
		filter = make(map[string]bool, len(cfg.PropertyIDs))
		for _, id := range cfg.PropertyIDs {
			filter[id] = true
		}
	}

	// Parse session expiry up front so /metrics can report it even
	// before the first successful tick. If decoding fails we log and
	// continue — the worst case is a missing alerting signal.
	var sessExp time.Time
	if exp, err := client.ParseJWTExpiry(cfg.SessionToken); err == nil {
		sessExp = exp
	} else {
		logger.Warn("could not parse session token expiry", "err", err)
	}

	p := &Poller{
		api:              cfg.API,
		interval:         interval,
		propertyFilter:   filter,
		logger:           logger,
		now:              now,
		inverters:        make(map[string][]client.Inverter),
		perProperty:      make(map[string]propertyState),
		sessionExpiresAt: sessExp,
	}
	p.snap.SessionExpiresAt = sessExp
	return p, nil
}

// Snapshot returns a defensive copy of the current cached state. Safe to
// call concurrently with Run.
func (p *Poller) Snapshot() Snapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	s := p.snap
	if s.Properties != nil {
		s.Properties = append([]PropertySnapshot(nil), s.Properties...)
	}
	return s
}

// Interval returns the configured poll interval (useful for logging).
func (p *Poller) Interval() time.Duration { return p.interval }

// Run drives the polling loop. The first tick fires immediately, then
// every Config.Interval. Returns nil on context cancellation OR on a
// terminal AuthError — the latter halts polling because retrying against
// a dead session just burns requests at a reCAPTCHA wall.
func (p *Poller) Run(ctx context.Context) error {
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			p.pollOnce(ctx, p.now())
			if p.authFailed {
				p.logger.Error("session is no longer authenticated; halting poller — paste fresh cookies and restart")
				return nil
			}
			timer.Reset(p.interval)
		}
	}
}

// pollOnce performs one poll cycle against `now`. Exported (lowercase)
// only within the package — tests use it to drive the loop without
// real timers.
func (p *Poller) pollOnce(ctx context.Context, now time.Time) {
	started := time.Now()

	if p.metadataLastRefresh.IsZero() || now.Sub(p.metadataLastRefresh) >= metadataMaxAge {
		if err := p.refreshMetadata(ctx); err != nil {
			if client.IsAuthError(err) {
				p.authFailed = true
			}
			p.recordTickFailure(now, fmt.Errorf("refresh metadata: %w", err))
			return
		}
		p.metadataLastRefresh = now
	}

	propSnaps := make([]PropertySnapshot, 0, len(p.propertyMeta))
	var firstErr error
	var fetched int
	for _, prop := range p.propertyMeta {
		if p.propertyFilter != nil && !p.propertyFilter[prop.ID] {
			continue
		}
		if !prop.IsReadyForEnergyProductionMonitoring {
			continue
		}
		ps, err := p.pollProperty(ctx, prop, now)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			p.logger.Warn("property poll failed", "property_id", prop.ID, "err", err)
			if client.IsAuthError(err) {
				p.authFailed = true
				p.recordTickFailure(now, err)
				return
			}
			continue
		}
		if invs, ok := p.inverters[prop.ID]; ok && len(invs) > 0 {
			ps.Inverter = invs[0]
		}
		propSnaps = append(propSnaps, ps)
		fetched++
	}

	elapsed := time.Since(started)
	p.recordTickResult(now, propSnaps, firstErr)
	p.logger.Debug("poll tick",
		"fetched", fetched,
		"skipped", len(p.propertyMeta)-fetched,
		"elapsed", elapsed.String(),
		"ok", firstErr == nil,
	)
}

func (p *Poller) refreshMetadata(ctx context.Context) error {
	props, err := p.api.GetProperties(ctx)
	if err != nil {
		return fmt.Errorf("get properties: %w", err)
	}
	p.propertyMeta = props

	acc, err := p.api.GetAccountInfo(ctx)
	if err != nil {
		return fmt.Errorf("get account info: %w", err)
	}
	// The account-info endpoint returns inverter metadata for a single
	// propertyInfo. For accounts with multiple properties this means we
	// only have inverter info for one of them — acceptable until/unless
	// the upstream API gains a per-property variant.
	if acc.Property.ID != "" && len(acc.Property.Inverters) > 0 {
		p.inverters[acc.Property.ID] = acc.Property.Inverters
	}
	return nil
}

func (p *Poller) pollProperty(ctx context.Context, prop client.Property, now time.Time) (PropertySnapshot, error) {
	loc, err := time.LoadLocation(prop.TimeZone)
	if err != nil {
		return PropertySnapshot{}, fmt.Errorf("load tz %q: %w", prop.TimeZone, err)
	}
	nowLocal := now.In(loc)
	todayLocal := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	todayKey := todayLocal.Format("2006-01-02")

	hourly, err := p.api.GetProduction(ctx, prop.ID, client.IntervalHourly, todayLocal, now, prop.TimeZone)
	if err != nil {
		return PropertySnapshot{}, fmt.Errorf("get hourly production: %w", err)
	}

	var todayTotal float64
	var latest HourSample
	var lastSampleAt time.Time
	respLoc, _ := hourly.Location() // may fail; we fall back to the property's loc
	if respLoc == nil {
		respLoc = loc
	}
	for _, s := range hourly.SystemEnergy {
		todayTotal += s.Value
		t, err := s.Time(respLoc)
		if err != nil {
			continue
		}
		if t.After(latest.Time) {
			latest = HourSample{Time: t, KWh: s.Value}
		}
		if t.After(lastSampleAt) {
			lastSampleAt = t
		}
	}

	// Should we (re)fetch yesterday? Only when the property-local
	// calendar date has changed (or this is our first tick for it).
	// The yesterday total is then stable for the rest of the day.
	prev, hadPrev := p.perProperty[prop.ID]
	needYesterday := !hadPrev || prev.LastSeenLocalDate != todayKey
	yesterdayKWh := prev.YesterdayKWh
	if needYesterday {
		yest := todayLocal.AddDate(0, 0, -1)
		daily, err := p.api.GetProduction(ctx, prop.ID, client.IntervalDaily, yest, todayLocal, prop.TimeZone)
		if err != nil {
			// Don't fail the whole tick for a yesterday fetch — log
			// and reuse whatever we had.
			p.logger.Warn("yesterday fetch failed", "property_id", prop.ID, "err", err)
		} else if len(daily.SystemEnergy) > 0 {
			yesterdayKWh = daily.SystemEnergy[0].Value
		}
	}
	p.perProperty[prop.ID] = propertyState{
		LastSeenLocalDate: todayKey,
		YesterdayKWh:      yesterdayKWh,
	}

	return PropertySnapshot{
		ID:           prop.ID,
		Street:       prop.StreetAddress,
		City:         prop.City,
		State:        prop.State,
		Timezone:     prop.TimeZone,
		TodayKWh:     todayTotal,
		YesterdayKWh: yesterdayKWh,
		LatestHour:   latest,
		LastSampleAt: lastSampleAt,
	}, nil
}

func (p *Poller) recordTickResult(now time.Time, props []PropertySnapshot, tickErr error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snap.LastPollAt = now
	p.snap.Properties = props
	p.snap.SessionExpiresAt = p.sessionExpiresAt
	p.snap.AuthFailed = p.authFailed
	if tickErr != nil {
		p.snap.OK = false
		p.snap.LastError = tickErr.Error()
	} else {
		p.snap.OK = true
		p.snap.LastError = ""
		p.snap.LastSuccessAt = now
	}
}

func (p *Poller) recordTickFailure(now time.Time, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Keep old Properties around so /metrics and /api/state can show
	// last-known values when a tick fails. The OK flag tells consumers
	// the data is stale.
	p.snap.LastPollAt = now
	p.snap.OK = false
	p.snap.LastError = err.Error()
	p.snap.SessionExpiresAt = p.sessionExpiresAt
	p.snap.AuthFailed = p.authFailed
	p.logger.Error("poll tick failed", "err", err)
}
