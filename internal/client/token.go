package client

import (
	"context"
	"errors"
)

// TokenProvider supplies the Session-Token and CSRF-Token values the
// client should use for each outbound request. Implementations may
// cache, watch files, or fetch from a secret store. The default
// implementation (StaticTokens) returns a fixed pair forever.
//
// Tokens is called before every authenticated request, so its
// implementations need to be fast — file-backed providers should
// cache by mtime, not re-parse every call.
type TokenProvider interface {
	Tokens(ctx context.Context) (sessionToken, csrfToken string, err error)
}

// StaticTokens implements TokenProvider with a fixed pair. Used as the
// fallback when callers don't specify a provider, and as the default
// test fixture.
type StaticTokens struct {
	Session string
	CSRF    string
}

// Tokens implements TokenProvider.
func (s StaticTokens) Tokens(_ context.Context) (string, string, error) {
	if s.Session == "" || s.CSRF == "" {
		return "", "", errors.New("StaticTokens: both Session and CSRF are required")
	}
	return s.Session, s.CSRF, nil
}
