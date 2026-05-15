# GAFfersTape

GAFfer's tape for your [GAF Energy](https://www.gaf.energy/) solar data — a small Go daemon that scrapes your `my.gaf.energy` portal and re-exposes the numbers in formats your tools actually speak: **Prometheus** (for Grafana) and **JSON** (for Home Assistant).

> ⚠ **Status: alpha — not yet tested end-to-end.** The daemon builds, runs, and serves the documented endpoints; it has *not* been verified against the live my.gaf.energy API with real credentials. See [docs/setup.md](docs/setup.md#status) for the full list of what has and hasn't been validated before relying on this.

## Why

The `my.gaf.energy` portal shows you pretty charts and nothing else. There is no documented API, no integration, no export. GAFfersTape bridges that gap so your solar production data lands in the same dashboards as the rest of your home telemetry.

## What it does

- **Authenticates** to the GAF Energy customer portal using session cookies you paste in (one-time setup, refreshed every few weeks).
- **Polls** the production endpoint on a sane interval (~15 min, matching upstream cadence).
- **Exposes** the data in two shapes:
  - `GET /metrics` — Prometheus text format, ready for any Prometheus server or Grafana Agent to scrape.
  - `GET /api/state` — compact JSON snapshot, designed for Home Assistant's `rest` sensor.

## Architecture at a glance

```
  ┌──────────────────┐    every 15m     ┌──────────────────┐
  │  my.gaf.energy   │ ◄──────────────  │   gafferstape    │
  │   (portal API)   │   session cookie │   (this repo)    │
  └──────────────────┘                  └────────┬─────────┘
                                                 │
                                ┌────────────────┴────────────────┐
                                ▼                                 ▼
                       GET /metrics                       GET /api/state
                       (Prometheus → Grafana)             (Home Assistant)
```

See [docs/architecture.md](docs/architecture.md) for the full design and [docs/api-notes.md](docs/api-notes.md) for what we know about the upstream API.

## Why not a "real" HA integration?

A custom Home Assistant integration is on the table down the line, but a single daemon that speaks both Prometheus and JSON is faster to build, easier to test, and works the same whether HA is present or not. Grafana doesn't care, HA doesn't care, and you can run it on whatever box already has Docker.

## Quickstart

1. Clone the repo and `cp config.example.yaml config.yaml`. Edit `listen`, `poll_interval`, etc. if the defaults don't suit you. Leave the cookie fields blank — they come from `.env` in step 3.
2. Log in to [my.gaf.energy](https://my.gaf.energy) in Firefox. Open dev-tools → Storage → Cookies → `wamy.gaf.energy`. Copy the values of `Session-Token` and `CSRF-Token`.
3. `cp .env.example .env` and paste them in.
4. `docker compose up -d --build`. The image builds to ~10 MB (distroless) and runs as a non-root user.
5. Verify:
   ```
   curl http://localhost:9876/healthz       # {"ok":true}
   curl http://localhost:9876/api/state     # JSON snapshot (issue #5)
   curl http://localhost:9876/metrics       # Prometheus text (issue #4)
   ```
6. Point Prometheus at `http://localhost:9876/metrics`, or wire Home Assistant to `/api/state` using [examples/homeassistant.yaml](examples/homeassistant.yaml).

Cookies expire about every 25 days. Watch `gaf_session_expires_seconds` (or the `session_expires_at` field in `/api/state`) and repeat steps 2–3 before they die.

## Documentation

- **[docs/setup.md](docs/setup.md)** — end-to-end walkthrough: cookie extraction, configuration, Prometheus and Grafana, Home Assistant, cookie rotation. Read the status callout at the top before relying on any of it.
- **[docs/troubleshooting.md](docs/troubleshooting.md)** — common failure modes and fixes.
- **[docs/architecture.md](docs/architecture.md)** — internal design and failure-mode table.
- **[docs/api-notes.md](docs/api-notes.md)** — what we know about the undocumented my.gaf.energy API.

## Home Assistant

`GET /api/state` returns the poller's snapshot as JSON; see [examples/homeassistant.yaml](examples/homeassistant.yaml) for a ready-to-paste `configuration.yaml` snippet wiring it up to HA's `rest` sensor.

```yaml
# configuration.yaml
rest: !include gafferstape.yaml  # copy of examples/homeassistant.yaml
```

## License

MIT — see [LICENSE](LICENSE).
