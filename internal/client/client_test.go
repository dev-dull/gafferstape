package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// capturedRequest records what the client sent so individual tests can
// assert request shape (path, query, headers, cookies) in addition to
// response decoding.
type capturedRequest struct {
	Method  string
	Path    string
	Query   url.Values
	Headers http.Header
	Cookies []*http.Cookie
}

func newCapturingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *capturedRequest) {
	t.Helper()
	cr := &capturedRequest{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cr.Method = r.Method
		cr.Path = r.URL.Path
		cr.Query = r.URL.Query()
		cr.Headers = r.Header.Clone()
		cr.Cookies = r.Cookies()
		handler(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, cr
}

func serveFixture(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}
}

func mustClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:      baseURL,
		SessionToken: "session-fixture",
		CSRFToken:    "csrf-fixture",
		UserAgent:    "gafferstape-test/1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hasCookie(cookies []*http.Cookie, name, value string) bool {
	for _, c := range cookies {
		if c.Name == name && c.Value == value {
			return true
		}
	}
	return false
}

// assertCommonRequest checks the cookies + headers we send on every
// authenticated GET. Each endpoint test reuses this.
func assertCommonRequest(t *testing.T, cr *capturedRequest) {
	t.Helper()
	if cr.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", cr.Method)
	}
	if got := cr.Headers.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := cr.Headers.Get("User-Agent"); got != "gafferstape-test/1" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := cr.Headers.Get("x-csrf-token"); got != "csrf-fixture" {
		t.Errorf("x-csrf-token = %q, want csrf-fixture", got)
	}
	if !hasCookie(cr.Cookies, "Session-Token", "session-fixture") {
		t.Errorf("missing Session-Token cookie; got %+v", cr.Cookies)
	}
	if !hasCookie(cr.Cookies, "CSRF-Token", "csrf-fixture") {
		t.Errorf("missing CSRF-Token cookie; got %+v", cr.Cookies)
	}
}

func TestGetProperties(t *testing.T) {
	ts, cr := newCapturingServer(t, serveFixture(t, "properties.json"))
	c := mustClient(t, ts.URL)

	props, err := c.GetProperties(context.Background())
	if err != nil {
		t.Fatalf("GetProperties: %v", err)
	}
	if len(props) != 1 {
		t.Fatalf("len(props) = %d, want 1", len(props))
	}
	p := props[0]
	if p.ID != "PROP-FIXTURE-1" {
		t.Errorf("ID = %q", p.ID)
	}
	if p.TimeZone != "America/Los_Angeles" {
		t.Errorf("TimeZone = %q", p.TimeZone)
	}
	if !p.IsReadyForEnergyProductionMonitoring {
		t.Error("IsReadyForEnergyProductionMonitoring = false")
	}
	if p.UTCOffsetInMinutes != nil {
		t.Errorf("UTCOffsetInMinutes = %v, want nil", p.UTCOffsetInMinutes)
	}

	if cr.Path != "/api/property/get-all" {
		t.Errorf("Path = %q", cr.Path)
	}
	if len(cr.Query) != 0 {
		t.Errorf("Query = %v, want empty", cr.Query)
	}
	assertCommonRequest(t, cr)
}

func TestGetAccountInfo(t *testing.T) {
	ts, cr := newCapturingServer(t, serveFixture(t, "account.json"))
	c := mustClient(t, ts.URL)

	acc, err := c.GetAccountInfo(context.Background())
	if err != nil {
		t.Fatalf("GetAccountInfo: %v", err)
	}

	if acc.HomeOwner.Email != "homeowner@example.invalid" {
		t.Errorf("Email = %q", acc.HomeOwner.Email)
	}
	if acc.HomeOwner.AssistingAdminName != nil {
		t.Errorf("AssistingAdminName = %v, want nil", acc.HomeOwner.AssistingAdminName)
	}
	// Embedded Property fields should be accessible directly.
	if acc.Property.ID != "PROP-FIXTURE-1" {
		t.Errorf("Property.ID = %q", acc.Property.ID)
	}
	if acc.Property.TimeZone != "America/Los_Angeles" {
		t.Errorf("Property.TimeZone = %q", acc.Property.TimeZone)
	}
	if len(acc.Property.Inverters) != 1 {
		t.Fatalf("Inverters = %d, want 1", len(acc.Property.Inverters))
	}
	inv := acc.Property.Inverters[0]
	if inv.SerialNumber != "FIXTURE-SERIAL-1" {
		t.Errorf("SerialNumber = %q", inv.SerialNumber)
	}
	if !inv.IsActive {
		t.Error("Inverter.IsActive = false")
	}

	if cr.Path != "/api/auth/get-account-info" {
		t.Errorf("Path = %q", cr.Path)
	}
	assertCommonRequest(t, cr)
}

func TestGetProductionHourly(t *testing.T) {
	ts, cr := newCapturingServer(t, serveFixture(t, "production-hourly.json"))
	c := mustClient(t, ts.URL)

	start := time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 5, 14, 23, 43, 0, 0, time.UTC)
	prod, err := c.GetProduction(context.Background(), "PROP-FIXTURE-1", IntervalHourly, start, end, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("GetProduction: %v", err)
	}

	if prod.Interval != "hourly" {
		t.Errorf("Interval = %q", prod.Interval)
	}
	if prod.Unit != "kWh" {
		t.Errorf("Unit = %q", prod.Unit)
	}
	if len(prod.SystemEnergy) != 11 {
		t.Fatalf("SystemEnergy len = %d, want 11", len(prod.SystemEnergy))
	}
	// Peak sample from fixture.
	if got := prod.SystemEnergy[6].Value; got != 1.36 {
		t.Errorf("SystemEnergy[6].Value = %v, want 1.36", got)
	}

	if cr.Path != "/api/energy/get-production" {
		t.Errorf("Path = %q", cr.Path)
	}
	if got := cr.Query.Get("PropertyId"); got != "PROP-FIXTURE-1" {
		t.Errorf("PropertyId = %q", got)
	}
	if got := cr.Query.Get("interval"); got != "hourly" {
		t.Errorf("interval = %q", got)
	}
	if got := cr.Query.Get("startTimestamp"); got != "2026-05-14T00:00:00Z" {
		t.Errorf("startTimestamp = %q", got)
	}
	if got := cr.Query.Get("endTimestamp"); got != "2026-05-14T23:43:00Z" {
		t.Errorf("endTimestamp = %q", got)
	}
	if got := cr.Query.Get("timezone"); got != "America/Los_Angeles" {
		t.Errorf("timezone = %q", got)
	}
	assertCommonRequest(t, cr)
}

func TestProductionTimeHelpers(t *testing.T) {
	ts, _ := newCapturingServer(t, serveFixture(t, "production-hourly.json"))
	c := mustClient(t, ts.URL)
	prod, err := c.GetProduction(context.Background(), "PROP-FIXTURE-1", IntervalHourly,
		time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 14, 23, 43, 0, 0, time.UTC),
		"America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}

	loc, err := prod.Location()
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if loc.String() != "America/Los_Angeles" {
		t.Errorf("Location = %q", loc.String())
	}
	first := prod.SystemEnergy[0]
	parsed, err := first.Time(loc)
	if err != nil {
		t.Fatalf("Time: %v", err)
	}
	want := time.Date(2026, 5, 14, 6, 0, 0, 0, loc)
	if !parsed.Equal(want) {
		t.Errorf("parsed = %s, want %s", parsed, want)
	}
}

func TestGetProductionValidatesArgs(t *testing.T) {
	c := mustClient(t, "http://127.0.0.1:1") // unused — calls should fail before hitting the wire
	ctx := context.Background()
	now := time.Now()

	cases := []struct {
		name string
		fn   func() error
	}{
		{"empty propertyID", func() error {
			_, err := c.GetProduction(ctx, "", IntervalHourly, now, now.Add(time.Hour), "UTC")
			return err
		}},
		{"empty interval", func() error {
			_, err := c.GetProduction(ctx, "P", "", now, now.Add(time.Hour), "UTC")
			return err
		}},
		{"empty tz", func() error {
			_, err := c.GetProduction(ctx, "P", IntervalHourly, now, now.Add(time.Hour), "")
			return err
		}},
		{"end before start", func() error {
			_, err := c.GetProduction(ctx, "P", IntervalHourly, now, now.Add(-time.Hour), "UTC")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestAuthErrorOn401(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)
	c := mustClient(t, ts.URL)

	_, err := c.GetProperties(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want AuthError", err)
	}
	if ae.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d", ae.StatusCode)
	}
	if !IsAuthError(err) {
		t.Error("IsAuthError = false")
	}
}

func TestAuthErrorOn403(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	t.Cleanup(ts.Close)
	c := mustClient(t, ts.URL)

	_, err := c.GetProperties(context.Background())
	if !IsAuthError(err) {
		t.Errorf("err = %v, want AuthError", err)
	}
}

func TestNon2xxNotAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(ts.Close)
	c := mustClient(t, ts.URL)

	_, err := c.GetProperties(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if IsAuthError(err) {
		t.Error("500 should not be classified as AuthError")
	}
}

func TestEnvelopeFailure(t *testing.T) {
	const body = `{"data":null,"isSuccess":false,"errorMessage":"something broke","validationErrors":null,"traceId":"trace-abc"}`
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(ts.Close)
	c := mustClient(t, ts.URL)

	_, err := c.GetProperties(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if IsAuthError(err) {
		t.Error("envelope failure should not be AuthError")
	}
	if !strings.Contains(err.Error(), "something broke") {
		t.Errorf("err = %v, want it to include errorMessage", err)
	}
	if !strings.Contains(err.Error(), "trace-abc") {
		t.Errorf("err = %v, want it to include traceId", err)
	}
}

func TestMalformedJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "not json{")
	}))
	t.Cleanup(ts.Close)
	c := mustClient(t, ts.URL)

	_, err := c.GetProperties(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewRejectsMissingTokens(t *testing.T) {
	if _, err := New(Config{CSRFToken: "x"}); err == nil {
		t.Error("missing SessionToken: expected error")
	}
	if _, err := New(Config{SessionToken: "x"}); err == nil {
		t.Error("missing CSRFToken: expected error")
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	if _, err := New(Config{BaseURL: "not a url", SessionToken: "s", CSRFToken: "c"}); err == nil {
		t.Error("expected error for invalid BaseURL")
	}
	if _, err := New(Config{BaseURL: "://nope", SessionToken: "s", CSRFToken: "c"}); err == nil {
		t.Error("expected error for malformed URL")
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(Config{SessionToken: "s", CSRFToken: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if c.base.Host != "wamy.gaf.energy" {
		t.Errorf("base.Host = %q, want wamy.gaf.energy", c.base.Host)
	}
	if c.userAgent == "" {
		t.Error("userAgent empty")
	}
}

func TestContextCancellation(t *testing.T) {
	// Server that hangs forever; the request should fail when ctx is cancelled.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)
	c := mustClient(t, ts.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.GetProperties(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}
