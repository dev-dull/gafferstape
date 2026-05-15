# Architecture

This document is the source of truth for how GAFfersTape is put together. If the code disagrees, the code is wrong (or this doc is stale — fix whichever is easier).

## Goals

- Get solar production numbers out of `my.gaf.energy` and into Prometheus and Home Assistant.
- Survive long-lived operation: ~25-day JWT, ~15-minute upstream update cadence.
- Be honest about what we don't get: no per-panel data, no battery state (the portal API doesn't expose them).

## Non-goals

- Reverse-engineering reCAPTCHA. Login is done manually in a real browser; cookies are pasted into config. See [api-notes.md](api-notes.md) for why.
- Re-implementing Grafana panels or HA dashboards. We expose metrics; you build the UI.
- Persisting a historical time-series ourselves. Prometheus does that.

## Process layout

A single Go binary, `gaf-exporter`, with these internal packages:

| Package | Responsibility |
|---|---|
| `internal/config` | Load YAML config, allow env-var overrides for secrets. |
| `internal/client` | HTTP client against `wamy.gaf.energy`: auth/cookie handling, the three endpoints we care about. |
| `internal/poller` | Background goroutine that refreshes data on `poll_interval` and caches the latest snapshot in memory. |
| `internal/server` | HTTP handlers: `/metrics`, `/api/state`, `/healthz`. Reads from the poller's cache; never blocks on upstream. |
| `cmd/gaf-exporter` | Wires it all together. |

The poller/server split matters: Prometheus may scrape every 15s, but upstream only updates ~hourly. Scrapes return cached values; the cache is refreshed on a separate schedule.

## Auth model

- Cookies (`Session-Token` JWT + `CSRF-Token`) are pasted into `config.yaml` after a manual Firefox login.
- The JWT's `exp` claim is decoded (no signature verification — we don't have the key, and we're not the issuer) and exposed as `gaf_session_expires_seconds` so you can alert *before* it dies.
- On 401/403 from upstream: log loudly, drop `gaf_up` to 0, do **not** attempt to re-authenticate. Auto-retry would just burn requests against a reCAPTCHA wall.

## CSRF handling (open question, pinned by first test)

The HAR shows `x-csrf-token` returned in a response header and a `CSRF-Token` cookie being set. It's not yet clear whether GET requests need the token echoed back as a header, as a cookie, or both. The first integration test against fixtures will pin this down; until then, the client will send both to be safe.

## Polling strategy

Every `poll_interval` (default 15m), for each property:

1. `GET /api/energy/get-production?interval=hourly&start=<today 00:00 local>&end=<now>` — gives us today's hourly buckets.
2. If we just crossed local midnight: also fetch yesterday's daily total as a stable "yesterday" gauge.

Once per 24h:

3. Refresh `/api/property/get-all` and `/api/auth/get-account-info` for inverter metadata.

## Prometheus metrics

| Metric | Type | Labels | Notes |
|---|---|---|---|
| `gaf_up` | gauge | — | 1 if last poll succeeded, 0 otherwise. |
| `gaf_scrape_errors_total` | counter | `endpoint` | Per-endpoint upstream errors. |
| `gaf_session_expires_seconds` | gauge | — | Seconds until JWT `exp`. Negative = already expired. |
| `gaf_last_sample_timestamp_seconds` | gauge | `property_id` | Unix time of the newest hourly bucket we've seen. |
| `gaf_energy_production_kwh_latest_hour` | gauge | `property_id`, `address` | kWh in the most recent complete hourly bucket. |
| `gaf_energy_production_kwh_today` | gauge | `property_id`, `address` | Sum of today's hourly buckets so far. |
| `gaf_energy_production_kwh_yesterday` | gauge | `property_id`, `address` | Stable once set at local midnight. |
| `gaf_inverter_info` | gauge=1 | `property_id`, `manufacturer`, `model`, `serial`, `active` | Info-style metric. |

**No lifetime counter.** The API doesn't expose one, and reconstructing it across restarts is more bugs than it's worth. Grafana can do running sums over the daily gauges.

## JSON state endpoint

`GET /api/state` returns the same data the metrics are built from, in a shape HA's `rest` sensor can consume without templating gymnastics:

```json
{
  "ok": true,
  "session_expires_at": "2026-06-09T12:00:00Z",
  "properties": [
    {
      "id": "<PROPERTY_ID>",
      "address": "...",
      "timezone": "America/Los_Angeles",
      "today_kwh": 12.4,
      "yesterday_kwh": 21.0,
      "latest_hour": { "time": "2026-05-14T15:00:00", "kwh": 1.31 },
      "inverter": {
        "manufacturer": "...",
        "model": "...",
        "serial": "...",
        "active": true
      }
    }
  ]
}
```

`ok: false` with an `error` field is returned when the last poll failed; HA can treat that as `unavailable`.

## Configuration

```yaml
listen: ":9876"
poll_interval: 15m
log_level: info

# Paste these from Firefox after logging in.
session_token: "<jwt cookie value>"
csrf_token: "<csrf cookie value>"

# Optional — auto-discovered from /api/property/get-all if omitted.
properties: []
```

Secrets can also be supplied via env vars (`GAFFERSTAPE_SESSION_TOKEN`, `GAFFERSTAPE_CSRF_TOKEN`) so Docker users don't have to bake them into the config file.

## Docker packaging

Multi-stage build: `golang:1.23-alpine` to compile, `gcr.io/distroless/static:nonroot` to ship. Binary listens on `:9876`. No volumes required (cache is in-memory); config is mounted read-only.

## Failure modes and how we handle them

| Failure | Detection | Response |
|---|---|---|
| Upstream 5xx | HTTP status | Increment `gaf_scrape_errors_total`, keep cache, retry next tick. |
| Upstream 401/403 | HTTP status | Set `gaf_up=0`, log error, do not retry until session is refreshed. |
| JWT expiring soon | `exp` claim < 7d | Log warn each poll; `gaf_session_expires_seconds` exposes it for alerting. |
| JWT already expired | `exp` claim in past | `gaf_up=0`, refuse to poll, log loud. |
| Network timeout | Context deadline | Same as 5xx. |
| Config missing cookies | Startup check | Log warning; run in /healthz-only mode (poller disabled). Lets ops verify the deployment before adding credentials. |
