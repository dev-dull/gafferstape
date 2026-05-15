package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ParseJWTExpiry decodes a JWT's payload and returns its exp claim as a
// time.Time.
//
// The signature is intentionally not verified: we're a consumer of tokens
// we didn't issue and have no signing key, and the only thing we use the
// claim for is exposing "how long until this session dies" as a metric.
// We're not making trust decisions, so a tampered exp is harmless —
// at worst the alerting fires at the wrong time.
func ParseJWTExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("invalid JWT: %d parts, want 3", len(parts))
	}
	payload, err := decodeJWTSegment(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parse JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("JWT has no exp claim")
	}
	return time.Unix(claims.Exp, 0), nil
}

// decodeJWTSegment handles both padded and unpadded base64url, since
// different JWT producers vary on padding.
func decodeJWTSegment(s string) ([]byte, error) {
	if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(s)
}
