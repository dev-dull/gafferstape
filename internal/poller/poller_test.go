package poller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
)

// ---------- Test doubles ----------

type apiCall struct {
	Method   string
	Property string
	Interval client.Interval
	Start    time.Time
	End      time.Time
}

type fakeAPI struct {
	mu sync.Mutex

	properties    []client.Property
	account       client.Account
	propertiesErr error
	accountErr    error

	// productionFunc fully controls GetProduction responses. Tests
	// install this to simulate per-property success/error or to vary
	// data between ticks.
	productionFunc func(propertyID string, interval client.Interval, start, end time.Time, tz string) (client.Production, error)

	calls []apiCall
}

func (f *fakeAPI) record(c apiCall) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
}

func (f *fakeAPI) callsCopy() []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apiCall(nil), f.calls...)
}

func (f *fakeAPI) count(method string) int {
	n := 0
	for _, c := range f.callsCopy() {
		if c.Method == method {
			n++
		}
	}
	return n
}

func (f *fakeAPI) countProduction(interval client.Interval) int {
	n := 0
	for _, c := range f.callsCopy() {
		if c.Method == "GetProduction" && c.Interval == interval {
			n++
		}
	}
	return n
}

func (f *fakeAPI) GetProperties(_ context.Context) ([]client.Property, error) {
	f.record(apiCall{Method: "GetProperties"})
	f.mu.Lock()
	err := f.propertiesErr
	props := f.properties
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return props, nil
}

func (f *fakeAPI) GetAccountInfo(_ context.Context) (client.Account, error) {
	f.record(apiCall{Method: "GetAccountInfo"})
	f.mu.Lock()
	err := f.accountErr
	acc := f.account
	f.mu.Unlock()
	if err != nil {
		return client.Account{}, err
	}
	return acc, nil
}

func (f *fakeAPI) GetProduction(_ context.Context, propertyID string, interval client.Interval, start, end time.Time, tz string) (client.Production, error) {
	f.record(apiCall{Method: "GetProduction", Property: propertyID, Interval: interval, Start: start, End: end})
	f.mu.Lock()
	fn := f.productionFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(propertyID, interval, start, end, tz)
	}
	return client.Production{}, errors.New("productionFunc not installed")
}

// ---------- Helpers ----------

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func makeJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{"exp": exp.Unix()})
	if err != nil {
		t.Fatal(err)
	}
	p := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + p + ".not-a-real-signature"
}

func laProperty(id string) client.Property {
	return client.Property{
		ID:                                   id,
		StreetAddress:                        "1 Example Way",
		City:                                 "Example City",
		State:                                "OR",
		ZipCode:                              "00000",
		TimeZone:                             "America/Los_Angeles",
		IsReadyForEnergyProductionMonitoring: true,
	}
}

func defaultProductionFunc(_ string, interval client.Interval, start, _ time.Time, tz string) (client.Production, error) {
	switch interval {
	case client.IntervalHourly:
		return client.Production{
			StartTimestamp: start.Format("2006-01-02T15:04:05"),
			TimePeriod:     "day",
			Interval:       "hourly",
			Unit:           "kWh",
			Timezone:       tz,
			SystemEnergy: []client.EnergySample{
				{Date: "2026-05-14T06:00:00", Value: 0.5},
				{Date: "2026-05-14T07:00:00", Value: 1.5},
				{Date: "2026-05-14T08:00:00", Value: 2.0},
			},
		}, nil
	case client.IntervalDaily:
		return client.Production{
			Timezone: tz,
			Interval: "daily",
			Unit:     "kWh",
			SystemEnergy: []client.EnergySample{
				{Date: start.Format("2006-01-02T15:04:05"), Value: 21.0},
			},
		}, nil
	}
	return client.Production{}, fmt.Errorf("unexpected interval %q", interval)
}

func newFake() *fakeAPI {
	return &fakeAPI{
		properties: []client.Property{laProperty("P1")},
		account: client.Account{
			Property: client.PropertyWithInverters{
				Property: client.Property{ID: "P1"},
				Inverters: []client.Inverter{{
					ID:           "INV1",
					Manufacturer: "Delta",
					ModelNumber:  "TEST-MODEL",
					SerialNumber: "SER-1",
					IsActive:     true,
				}},
			},
		},
		productionFunc: defaultProductionFunc,
	}
}

func newTestPoller(t *testing.T, api *fakeAPI, mod func(*Config)) *Poller {
	t.Helper()
	cfg := Config{
		API:              api,
		Interval:         time.Minute,
		SessionToken:     makeJWT(t, time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)),
		Logger:           discardLogger(),
		RetryMaxAttempts: 1, // tests that want retries opt in via mod
		RetryBaseBackoff: time.Millisecond,
	}
	if mod != nil {
		mod(&cfg)
	}
	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// ---------- Construction / validation ----------

func TestNewRequiresAPI(t *testing.T) {
	_, err := New(Config{SessionToken: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewRequiresSessionToken(t *testing.T) {
	_, err := New(Config{API: newFake()})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewParsesSessionExpiry(t *testing.T) {
	exp := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	p, err := New(Config{
		API:          newFake(),
		SessionToken: makeJWT(t, exp),
		Logger:       discardLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := p.Snapshot().SessionExpiresAt
	if !got.Equal(exp) {
		t.Errorf("SessionExpiresAt = %s, want %s", got, exp)
	}
}

func TestNewSurvivesBadJWT(t *testing.T) {
	p, err := New(Config{
		API:          newFake(),
		SessionToken: "garbage",
		Logger:       discardLogger(),
	})
	if err != nil {
		t.Fatal("New should not fail on unparseable JWT")
	}
	if !p.Snapshot().SessionExpiresAt.IsZero() {
		t.Error("SessionExpiresAt should be zero on bad JWT")
	}
}

// ---------- First tick ----------

func TestFirstTick(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, nil)

	now := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC) // 10am PT
	p.pollOnce(context.Background(), now)

	if api.count("GetProperties") != 1 {
		t.Errorf("GetProperties calls = %d, want 1", api.count("GetProperties"))
	}
	if api.count("GetAccountInfo") != 1 {
		t.Errorf("GetAccountInfo calls = %d, want 1", api.count("GetAccountInfo"))
	}
	if api.countProduction(client.IntervalHourly) != 1 {
		t.Errorf("hourly calls = %d, want 1", api.countProduction(client.IntervalHourly))
	}
	if api.countProduction(client.IntervalDaily) != 1 {
		t.Errorf("daily calls = %d, want 1 (yesterday capture)", api.countProduction(client.IntervalDaily))
	}

	snap := p.Snapshot()
	if !snap.OK {
		t.Errorf("OK = false, LastError = %q", snap.LastError)
	}
	if len(snap.Properties) != 1 {
		t.Fatalf("Properties = %d, want 1", len(snap.Properties))
	}
	ps := snap.Properties[0]
	if ps.ID != "P1" {
		t.Errorf("ID = %q", ps.ID)
	}
	if ps.TodayKWh != 4.0 { // 0.5 + 1.5 + 2.0
		t.Errorf("TodayKWh = %v, want 4.0", ps.TodayKWh)
	}
	if ps.YesterdayKWh != 21.0 {
		t.Errorf("YesterdayKWh = %v, want 21.0", ps.YesterdayKWh)
	}
	if ps.LatestHour.KWh != 2.0 {
		t.Errorf("LatestHour.KWh = %v, want 2.0", ps.LatestHour.KWh)
	}
	if ps.Inverter.SerialNumber != "SER-1" {
		t.Errorf("Inverter.SerialNumber = %q", ps.Inverter.SerialNumber)
	}
}

// ---------- Metadata caching ----------

func TestSecondTickReusesMetadata(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, nil)

	t0 := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	p.pollOnce(context.Background(), t0)
	p.pollOnce(context.Background(), t0.Add(5*time.Minute))

	if got := api.count("GetProperties"); got != 1 {
		t.Errorf("GetProperties = %d, want 1 (metadata should be cached)", got)
	}
	if got := api.count("GetAccountInfo"); got != 1 {
		t.Errorf("GetAccountInfo = %d, want 1", got)
	}
	if got := api.countProduction(client.IntervalHourly); got != 2 {
		t.Errorf("hourly = %d, want 2 (one per tick)", got)
	}
	// Same local day, no rollover — yesterday should NOT be refetched.
	if got := api.countProduction(client.IntervalDaily); got != 1 {
		t.Errorf("daily = %d, want 1 (yesterday is stable for the day)", got)
	}
}

func TestMetadataRefreshAfter24h(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, nil)

	t0 := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	p.pollOnce(context.Background(), t0)
	p.pollOnce(context.Background(), t0.Add(25*time.Hour))

	if got := api.count("GetProperties"); got != 2 {
		t.Errorf("GetProperties = %d, want 2", got)
	}
	if got := api.count("GetAccountInfo"); got != 2 {
		t.Errorf("GetAccountInfo = %d, want 2", got)
	}
}

// ---------- Midnight rollover ----------

func TestMidnightRolloverRefetchesYesterday(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	api := newFake()
	p := newTestPoller(t, api, nil)

	// 11pm PT on May 14 → "today" is May 14
	t1 := time.Date(2026, 5, 14, 23, 0, 0, 0, loc)
	// 1am PT on May 15 → "today" is now May 15
	t2 := time.Date(2026, 5, 15, 1, 0, 0, 0, loc)

	p.pollOnce(context.Background(), t1)
	p.pollOnce(context.Background(), t2)

	if got := api.countProduction(client.IntervalDaily); got != 2 {
		t.Errorf("daily = %d, want 2 (rollover should trigger yesterday refetch)", got)
	}
}

func TestNoRolloverWithinDay(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	api := newFake()
	p := newTestPoller(t, api, nil)

	t1 := time.Date(2026, 5, 14, 8, 0, 0, 0, loc)
	t2 := time.Date(2026, 5, 14, 22, 0, 0, 0, loc) // same local day

	p.pollOnce(context.Background(), t1)
	p.pollOnce(context.Background(), t2)

	if got := api.countProduction(client.IntervalDaily); got != 1 {
		t.Errorf("daily = %d, want 1 (no rollover, yesterday is stable)", got)
	}
}

// ---------- Auth failure ----------

func TestAuthErrorOnMetadataHaltsPolling(t *testing.T) {
	api := newFake()
	api.propertiesErr = &client.AuthError{StatusCode: 401, URL: "/api/property/get-all"}
	p := newTestPoller(t, api, nil)

	now := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	p.pollOnce(context.Background(), now)

	snap := p.Snapshot()
	if !snap.AuthFailed {
		t.Error("AuthFailed = false, want true")
	}
	if snap.OK {
		t.Error("OK = true, want false")
	}
	if !strings.Contains(snap.LastError, "authentication failed") {
		t.Errorf("LastError = %q, want auth-related", snap.LastError)
	}
}

func TestAuthErrorOnProductionHaltsPolling(t *testing.T) {
	api := newFake()
	api.productionFunc = func(_ string, _ client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		return client.Production{}, &client.AuthError{StatusCode: 401, URL: "/api/energy/get-production"}
	}
	p := newTestPoller(t, api, nil)

	p.pollOnce(context.Background(), time.Now())

	snap := p.Snapshot()
	if !snap.AuthFailed {
		t.Error("AuthFailed = false")
	}
	if snap.OK {
		t.Error("OK = true, want false")
	}
}

func TestRunStopsOnAuthFailure(t *testing.T) {
	api := newFake()
	api.propertiesErr = &client.AuthError{StatusCode: 401}
	p := newTestPoller(t, api, func(c *Config) {
		c.Interval = 10 * time.Millisecond
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return after auth failure within 1s")
	}
	if !p.Snapshot().AuthFailed {
		t.Error("AuthFailed = false")
	}
}

// ---------- Transient failure ----------

func TestTransientErrorMarksTickFailedPreservesLastSuccess(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, nil)

	// Tick 1 succeeds.
	t0 := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	p.pollOnce(context.Background(), t0)
	if !p.Snapshot().OK {
		t.Fatal("tick 1 did not succeed")
	}

	// Tick 2: hourly production fails for the only property. The
	// property is absent from this tick's snapshot (Prometheus will
	// see its metrics go absent — that's the desired signal). OK=false,
	// LastError set, but LastSuccessAt still points to tick 1.
	api.productionFunc = func(_ string, interval client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		if interval == client.IntervalHourly {
			return client.Production{}, errors.New("upstream blew up")
		}
		return client.Production{}, nil
	}
	t1 := t0.Add(5 * time.Minute)
	p.pollOnce(context.Background(), t1)

	snap := p.Snapshot()
	if snap.OK {
		t.Error("OK = true after upstream error")
	}
	if !strings.Contains(snap.LastError, "upstream blew up") {
		t.Errorf("LastError = %q, want it to mention upstream error", snap.LastError)
	}
	if !snap.LastSuccessAt.Equal(t0) {
		t.Errorf("LastSuccessAt = %s, want %s (tick 1)", snap.LastSuccessAt, t0)
	}
	if !snap.LastPollAt.Equal(t1) {
		t.Errorf("LastPollAt = %s, want %s (tick 2)", snap.LastPollAt, t1)
	}
}

func TestMetadataFailurePreservesLastProperties(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, nil)

	// Tick 1 succeeds and seeds Properties.
	t0 := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	p.pollOnce(context.Background(), t0)
	if len(p.Snapshot().Properties) != 1 {
		t.Fatal("tick 1 did not seed properties")
	}

	// Force the next tick into a metadata refresh by jumping 25h ahead,
	// then break GetProperties. The poller should keep last-known
	// Properties so /metrics and /api/state can still serve stale data.
	api.propertiesErr = errors.New("upstream blew up")
	p.pollOnce(context.Background(), t0.Add(25*time.Hour))

	snap := p.Snapshot()
	if snap.OK {
		t.Error("OK = true after metadata failure")
	}
	if len(snap.Properties) != 1 {
		t.Errorf("Properties = %d, want 1 (stale-but-visible after metadata failure)", len(snap.Properties))
	}
}

// ---------- Property filtering ----------

func TestPropertyFilter(t *testing.T) {
	api := newFake()
	api.properties = []client.Property{laProperty("P1"), laProperty("P2"), laProperty("P3")}
	p := newTestPoller(t, api, func(c *Config) {
		c.PropertyIDs = []string{"P2"}
	})

	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	snap := p.Snapshot()
	if len(snap.Properties) != 1 {
		t.Fatalf("Properties = %d, want 1", len(snap.Properties))
	}
	if snap.Properties[0].ID != "P2" {
		t.Errorf("ID = %q", snap.Properties[0].ID)
	}

	// Verify only P2 had production fetched.
	for _, c := range api.callsCopy() {
		if c.Method == "GetProduction" && c.Property != "P2" {
			t.Errorf("unexpected production call for %q", c.Property)
		}
	}
}

func TestSkipPropertiesNotReady(t *testing.T) {
	api := newFake()
	notReady := laProperty("P-NOT-READY")
	notReady.IsReadyForEnergyProductionMonitoring = false
	api.properties = []client.Property{laProperty("P1"), notReady}

	p := newTestPoller(t, api, nil)
	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	snap := p.Snapshot()
	if len(snap.Properties) != 1 {
		t.Errorf("Properties = %d, want 1 (the not-ready one should be skipped)", len(snap.Properties))
	}
	if snap.Properties[0].ID != "P1" {
		t.Errorf("ID = %q", snap.Properties[0].ID)
	}
}

// ---------- Snapshot isolation ----------

func TestSnapshotIsIndependent(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, nil)
	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	snap1 := p.Snapshot()
	if len(snap1.Properties) == 0 {
		t.Fatal("expected at least 1 property")
	}
	snap1.Properties[0].TodayKWh = 999.0
	snap1.LastError = "tampered"

	snap2 := p.Snapshot()
	if snap2.Properties[0].TodayKWh == 999.0 {
		t.Error("internal state was mutated via returned snapshot")
	}
	if snap2.LastError == "tampered" {
		t.Error("LastError mutated externally")
	}
}

// ---------- Retry ----------

func TestRetrySucceedsAfterTransientFailure(t *testing.T) {
	api := newFake()
	failsLeft := 2
	api.productionFunc = func(_ string, interval client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		if interval == client.IntervalHourly && failsLeft > 0 {
			failsLeft--
			return client.Production{}, errors.New("transient 503")
		}
		return defaultProductionFunc("", interval, time.Time{}, time.Time{}, "")
	}
	p := newTestPoller(t, api, func(c *Config) {
		c.RetryMaxAttempts = 3
		c.RetryBaseBackoff = time.Millisecond
	})

	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	if !p.Snapshot().OK {
		t.Errorf("OK = false after successful retry; LastError = %q", p.Snapshot().LastError)
	}
	// Hourly was called 3 times (2 failures + 1 success).
	if got := api.countProduction(client.IntervalHourly); got != 3 {
		t.Errorf("hourly calls = %d, want 3 (2 retries + success)", got)
	}
	// And scrape errors counter reflects the 2 retried failures.
	if got := p.Snapshot().ScrapeErrors[opGetProductionHourly]; got != 2 {
		t.Errorf("scrape_errors[hourly] = %d, want 2", got)
	}
}

func TestRetryExhaustionFailsTick(t *testing.T) {
	api := newFake()
	api.productionFunc = func(_ string, interval client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		if interval == client.IntervalHourly {
			return client.Production{}, errors.New("persistent 503")
		}
		return client.Production{}, nil
	}
	p := newTestPoller(t, api, func(c *Config) {
		c.RetryMaxAttempts = 3
		c.RetryBaseBackoff = time.Millisecond
	})

	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	snap := p.Snapshot()
	if snap.OK {
		t.Error("OK = true after exhausted retries")
	}
	if got := api.countProduction(client.IntervalHourly); got != 3 {
		t.Errorf("hourly calls = %d, want 3 (max attempts)", got)
	}
	if got := snap.ScrapeErrors[opGetProductionHourly]; got != 3 {
		t.Errorf("scrape_errors[hourly] = %d, want 3", got)
	}
}

func TestRetrySkippedOnAuthError(t *testing.T) {
	api := newFake()
	api.productionFunc = func(_ string, _ client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		return client.Production{}, &client.AuthError{StatusCode: 401, URL: "/api/energy/get-production"}
	}
	p := newTestPoller(t, api, func(c *Config) {
		c.RetryMaxAttempts = 5 // would be 5 if we retried; we shouldn't
		c.RetryBaseBackoff = time.Millisecond
	})

	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	if got := api.countProduction(client.IntervalHourly); got != 1 {
		t.Errorf("hourly calls = %d, want 1 (AuthError must not be retried)", got)
	}
	if !p.Snapshot().AuthFailed {
		t.Error("AuthFailed = false; AuthError should halt the poller")
	}
}

func TestRetryRespectsContextCancellation(t *testing.T) {
	api := newFake()
	api.productionFunc = func(_ string, _ client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		return client.Production{}, errors.New("transient")
	}
	p := newTestPoller(t, api, func(c *Config) {
		c.RetryMaxAttempts = 5
		c.RetryBaseBackoff = 200 * time.Millisecond
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	p.pollOnce(ctx, time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))
	elapsed := time.Since(start)

	// With backoff sequence 200ms, 400ms, 800ms, 1.6s and 5 attempts,
	// the full retry chain would take well over a second. Cancelling
	// mid-flight should cut this short.
	if elapsed > 500*time.Millisecond {
		t.Errorf("pollOnce took %s; ctx cancellation should have aborted retries faster", elapsed)
	}
}

// ---------- Session expiry ----------

func TestSessionExpiryWarnsBelow6Hours(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	now := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	api := newFake()
	p := newTestPoller(t, api, func(c *Config) {
		c.Logger = logger
		c.SessionToken = makeJWT(t, now.Add(3*time.Hour)) // 3h out — inside <6h warn window
	})
	p.pollOnce(context.Background(), now)

	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) || !strings.Contains(out, "session expires soon") {
		t.Errorf("expected WARN \"session expires soon\" in logs; got:\n%s", out)
	}
}

func TestSessionExpiryErrorsBelow1Hour(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	now := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	api := newFake()
	p := newTestPoller(t, api, func(c *Config) {
		c.Logger = logger
		c.SessionToken = makeJWT(t, now.Add(30*time.Minute)) // 30m out — inside <1h error window
	})
	p.pollOnce(context.Background(), now)

	out := buf.String()
	if !strings.Contains(out, `"level":"ERROR"`) || !strings.Contains(out, "session expires very soon") {
		t.Errorf("expected ERROR \"session expires very soon\" in logs; got:\n%s", out)
	}
	// Poll should still proceed.
	if !p.Snapshot().OK {
		t.Errorf("OK = false despite token still valid; LastError = %q", p.Snapshot().LastError)
	}
}

func TestSessionExpiredHaltsPolling(t *testing.T) {
	now := time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC)
	api := newFake()
	p := newTestPoller(t, api, func(c *Config) {
		c.SessionToken = makeJWT(t, now.Add(-time.Hour)) // already expired
	})
	p.pollOnce(context.Background(), now)

	snap := p.Snapshot()
	if !snap.AuthFailed {
		t.Error("AuthFailed = false; expired token should halt the poller")
	}
	if snap.OK {
		t.Error("OK = true after expired session")
	}
	if !strings.Contains(snap.LastError, "expired") {
		t.Errorf("LastError = %q, want it to mention expiry", snap.LastError)
	}
	// Upstream should not have been called at all.
	if got := api.count("GetProperties"); got != 0 {
		t.Errorf("GetProperties called %d times; expired session must skip the upstream entirely", got)
	}
}

func TestSnapshotIncludesScrapeErrors(t *testing.T) {
	api := newFake()
	api.productionFunc = func(_ string, interval client.Interval, _, _ time.Time, _ string) (client.Production, error) {
		if interval == client.IntervalHourly {
			return client.Production{}, errors.New("transient")
		}
		return client.Production{}, nil
	}
	p := newTestPoller(t, api, func(c *Config) {
		c.RetryMaxAttempts = 2
		c.RetryBaseBackoff = time.Millisecond
	})
	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	se := p.Snapshot().ScrapeErrors
	if se == nil {
		t.Fatal("ScrapeErrors map is nil")
	}
	if got := se[opGetProductionHourly]; got != 2 {
		t.Errorf("ScrapeErrors[hourly] = %d, want 2", got)
	}
	// Other ops should be absent (no errors).
	if _, ok := se[opGetProperties]; ok {
		t.Errorf("ScrapeErrors should not include successful ops; got entry for %q", opGetProperties)
	}
}

// ---------- Token hot-reload ----------

// mutableTokens is a goroutine-safe TokenProvider whose returned pair
// can be swapped between calls — used to simulate the user editing
// config.yaml mid-run.
type mutableTokens struct {
	mu         sync.Mutex
	sess, csrf string
}

func (m *mutableTokens) set(sess, csrf string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sess, m.csrf = sess, csrf
}

func (m *mutableTokens) Tokens(_ context.Context) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sess, m.csrf, nil
}

func TestPollerHotReloadsSessionExpiry(t *testing.T) {
	initialExp := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	updatedExp := time.Date(2027, 2, 15, 9, 0, 0, 0, time.UTC)

	tokens := &mutableTokens{
		sess: makeJWT(t, initialExp),
		csrf: "irrelevant-for-poller",
	}
	api := newFake()
	p := newTestPoller(t, api, func(c *Config) {
		c.TokenProvider = tokens
		c.SessionToken = "" // provider wins; field unused
	})

	if got := p.Snapshot().SessionExpiresAt; !got.Equal(initialExp) {
		t.Fatalf("initial SessionExpiresAt = %s, want %s", got, initialExp)
	}

	// Simulate the user editing config.yaml in place — provider now
	// returns a JWT with a later exp.
	tokens.set(makeJWT(t, updatedExp), "irrelevant-for-poller-2")

	p.pollOnce(context.Background(), time.Date(2026, 5, 14, 17, 0, 0, 0, time.UTC))

	if got := p.Snapshot().SessionExpiresAt; !got.Equal(updatedExp) {
		t.Errorf("after token swap, SessionExpiresAt = %s, want %s", got, updatedExp)
	}
}

// ---------- Run loop integration ----------

func TestRunStopsOnContextCancel(t *testing.T) {
	api := newFake()
	p := newTestPoller(t, api, func(c *Config) {
		c.Interval = 20 * time.Millisecond
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Give it time to tick at least once.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}

	if api.count("GetProperties") < 1 {
		t.Errorf("expected at least one metadata fetch, got %d", api.count("GetProperties"))
	}
}
