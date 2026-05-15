package poller

import (
	"context"
	"time"

	"github.com/dev-dull/gafferstape/internal/client"
)

// retryConfig governs the retry helper. All zero-valued fields are
// substituted with defaults in New().
type retryConfig struct {
	maxAttempts int
	base        time.Duration // first wait
	maxBackoff  time.Duration // cap on the exponential growth
}

// retryAPI runs fn with exponential backoff, returning the first
// successful result or the final error after maxAttempts. AuthErrors
// short-circuit — there's no point retrying against a reCAPTCHA-guarded
// session that has gone bad. Every failure (including the terminal one)
// bumps p.scrapeErrors[op] so /metrics reflects retry pressure, not
// just last-tick outcomes.
//
// Free function rather than method because Go doesn't allow type
// parameters on methods.
func retryAPI[T any](
	ctx context.Context,
	p *Poller,
	op string,
	fn func(context.Context) (T, error),
) (T, error) {
	var zero T
	var lastErr error
	backoff := p.retry.base

	for attempt := 1; attempt <= p.retry.maxAttempts; attempt++ {
		v, err := fn(ctx)
		if err == nil {
			return v, nil
		}
		p.scrapeErrors[op]++
		if client.IsAuthError(err) {
			return zero, err
		}
		lastErr = err
		if attempt == p.retry.maxAttempts {
			break
		}

		wait := backoff
		if wait > p.retry.maxBackoff {
			wait = p.retry.maxBackoff
		}
		p.logger.Warn("retrying upstream call",
			"op", op,
			"attempt", attempt,
			"next_in", wait.String(),
			"err", err,
		)
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-time.After(wait):
		}
		backoff *= 2
	}
	return zero, lastErr
}

// maxDuration returns the larger of a and b.
func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func firstPositive(a, fallback int) int {
	if a > 0 {
		return a
	}
	return fallback
}

func firstPositiveDuration(a, fallback time.Duration) time.Duration {
	if a > 0 {
		return a
	}
	return fallback
}
