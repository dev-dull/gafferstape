package client

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// makeJWT builds a syntactically valid JWT with the given claims. The
// signature is garbage — ParseJWTExpiry doesn't verify it.
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sig := base64.RawURLEncoding.EncodeToString([]byte("not-a-real-signature"))
	return header + "." + payload + "." + sig
}

func TestParseJWTExpiry(t *testing.T) {
	want := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	token := makeJWT(t, map[string]any{
		"exp": want.Unix(),
		"iss": "test",
		"sub": "test-subject",
	})

	got, err := ParseJWTExpiry(token)
	if err != nil {
		t.Fatalf("ParseJWTExpiry: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestParseJWTExpiryMalformed(t *testing.T) {
	cases := []string{
		"",
		"one-part",
		"two.parts",
		"four.parts.here.too",
		"header.!!!notbase64!!!.sig",
		"header." + base64.RawURLEncoding.EncodeToString([]byte("not-json")) + ".sig",
	}
	for _, tok := range cases {
		t.Run(tok, func(t *testing.T) {
			if _, err := ParseJWTExpiry(tok); err == nil {
				t.Errorf("expected error for %q", tok)
			}
		})
	}
}

func TestParseJWTExpiryMissingExp(t *testing.T) {
	token := makeJWT(t, map[string]any{"iss": "test"})
	_, err := ParseJWTExpiry(token)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "exp") {
		t.Errorf("err = %v, want mention of exp", err)
	}
}

func TestParseJWTExpiryPaddedBase64(t *testing.T) {
	// Some encoders use padded base64; ParseJWTExpiry should still cope.
	header := base64.URLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(map[string]any{"exp": int64(1900000000)})
	payload := base64.URLEncoding.EncodeToString(payloadJSON)
	sig := base64.URLEncoding.EncodeToString([]byte("sig"))
	token := header + "." + payload + "." + sig

	got, err := ParseJWTExpiry(token)
	if err != nil {
		t.Fatalf("ParseJWTExpiry: %v", err)
	}
	if got.Unix() != 1900000000 {
		t.Errorf("got %d, want 1900000000", got.Unix())
	}
}
