// Package client wraps the my.gaf.energy customer-portal API. It assumes
// the caller has already logged in via a browser and pasted the session
// cookies into config — see docs/setup.md (arrives with issue #7) for
// the user-facing walkthrough.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

const (
	// DefaultBaseURL is the production GAF Energy API host.
	DefaultBaseURL = "https://wamy.gaf.energy"

	cookieSession = "Session-Token"
	cookieCSRF    = "CSRF-Token"
	headerCSRF    = "x-csrf-token"

	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "gafferstape/0.1.0-dev"
)

// Config configures a new Client.
type Config struct {
	// BaseURL of the API. Defaults to DefaultBaseURL.
	BaseURL string

	// SessionToken is the Session-Token cookie value (a JWT). Required.
	SessionToken string

	// CSRFToken is the CSRF-Token cookie value. Sent both as the
	// CSRF-Token cookie and as the x-csrf-token request header — the
	// portal validates both, as confirmed in docs/api-notes.md. Required.
	CSRFToken string

	// HTTPClient lets callers swap in their own transport (e.g. tests,
	// proxies). If nil, a client with a 30s timeout and a cookie jar is
	// constructed.
	HTTPClient *http.Client

	// UserAgent string sent on every request. Defaults to
	// "gafferstape/<version>".
	UserAgent string

	// Logger receives debug-level request logs. If nil, logs are discarded.
	Logger *slog.Logger
}

// Client talks to the GAF Energy customer portal API.
type Client struct {
	base      *url.URL
	http      *http.Client
	csrfTok   string
	userAgent string
	logger    *slog.Logger
}

// New constructs a Client from cfg. SessionToken and CSRFToken are required.
func New(cfg Config) (*Client, error) {
	if cfg.SessionToken == "" {
		return nil, errors.New("client: SessionToken is required")
	}
	if cfg.CSRFToken == "" {
		return nil, errors.New("client: CSRFToken is required")
	}

	rawBase := cfg.BaseURL
	if rawBase == "" {
		rawBase = DefaultBaseURL
	}
	base, err := url.Parse(rawBase)
	if err != nil {
		return nil, fmt.Errorf("parse BaseURL: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("invalid BaseURL %q", rawBase)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if httpClient.Jar == nil {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("cookie jar: %w", err)
		}
		httpClient.Jar = jar
	}
	httpClient.Jar.SetCookies(base, []*http.Cookie{
		{Name: cookieSession, Value: cfg.SessionToken, Path: "/"},
		{Name: cookieCSRF, Value: cfg.CSRFToken, Path: "/"},
	})

	ua := cfg.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return &Client{
		base:      base,
		http:      httpClient,
		csrfTok:   cfg.CSRFToken,
		userAgent: ua,
		logger:    logger,
	}, nil
}

// do issues an authenticated GET. On 401/403 it returns an *AuthError so
// callers can distinguish session death from transient upstream failure.
// The caller owns resp.Body and must close it.
func (c *Client) do(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	u := *c.base
	u.Path = path
	if query != nil {
		u.RawQuery = query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set(headerCSRF, c.csrfTok)

	c.logger.Debug("api request", "url", u.String())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_ = resp.Body.Close()
		return nil, &AuthError{StatusCode: resp.StatusCode, URL: u.Path}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("GET %s: status %d: %s", path, resp.StatusCode, body)
	}
	return resp, nil
}

// envelope wraps every JSON response from the portal API. The interesting
// payload lives in Data; the rest is success/error metadata.
type envelope[T any] struct {
	Data             T               `json:"data"`
	IsSuccess        bool            `json:"isSuccess"`
	ErrorMessage     *string         `json:"errorMessage"`
	ValidationErrors json.RawMessage `json:"validationErrors"`
	TraceID          string          `json:"traceId"`
}

// fetch issues an authenticated GET and unwraps the response envelope
// into T. Free function (not method) because Go doesn't allow type
// parameters on methods.
func fetch[T any](ctx context.Context, c *Client, path string, query url.Values) (T, error) {
	var zero T
	resp, err := c.do(ctx, path, query)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	var env envelope[T]
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return zero, fmt.Errorf("decode %s: %w", path, err)
	}
	if !env.IsSuccess {
		msg := "request failed"
		if env.ErrorMessage != nil {
			msg = *env.ErrorMessage
		}
		return zero, fmt.Errorf("%s (traceId=%s): %s", path, env.TraceID, msg)
	}
	return env.Data, nil
}

// AuthError is returned when the portal rejects the session cookies
// (401 or 403). Callers (the poller, in particular) use this to stop
// retrying — auto-retry on a dead session just burns requests against
// a reCAPTCHA wall.
type AuthError struct {
	StatusCode int
	URL        string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("authentication failed (status %d)", e.StatusCode)
}

// IsAuthError reports whether err is or wraps an *AuthError.
func IsAuthError(err error) bool {
	var ae *AuthError
	return errors.As(err, &ae)
}
