package client

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Interval is the bucket size for /api/energy/get-production.
type Interval string

const (
	IntervalHourly  Interval = "hourly"
	IntervalDaily   Interval = "daily"
	IntervalWeekly  Interval = "weekly"
	IntervalMonthly Interval = "monthly"
	IntervalYearly  Interval = "yearly"
)

// requestTimestampFormat matches the UTC ISO 8601 form the portal expects
// for query parameters, e.g. "2026-05-14T00:00:00Z".
const requestTimestampFormat = "2006-01-02T15:04:05Z"

// GetProduction fetches production buckets in [start, end] for propertyID,
// at the given interval. tz is the IANA timezone the server should bucket
// against — typically the property's own TimeZone from GetProperties.
//
// Lifetime mode (timePeriod=lifetime in the portal) is not exposed: the
// poller doesn't need it, and supporting it would complicate the signature.
func (c *Client) GetProduction(ctx context.Context, propertyID string, interval Interval, start, end time.Time, tz string) (Production, error) {
	switch {
	case propertyID == "":
		return Production{}, errors.New("GetProduction: propertyID is empty")
	case interval == "":
		return Production{}, errors.New("GetProduction: interval is empty")
	case tz == "":
		return Production{}, errors.New("GetProduction: tz is empty")
	case end.Before(start):
		return Production{}, fmt.Errorf("GetProduction: end %s is before start %s", end, start)
	}

	q := url.Values{}
	q.Set("PropertyId", propertyID)
	q.Set("interval", string(interval))
	q.Set("startTimestamp", start.UTC().Format(requestTimestampFormat))
	q.Set("endTimestamp", end.UTC().Format(requestTimestampFormat))
	q.Set("timezone", tz)
	return fetch[Production](ctx, c, "/api/energy/get-production", q)
}
