# GAFfersTape

GAFfer's tape for your [GAF Energy](https://www.gaf.energy/) solar data — a small Go daemon that scrapes your `my.gaf.energy` portal and re-exposes the numbers in formats your tools actually speak: **Prometheus** (for Grafana) and **JSON** (for Home Assistant).

> ⚠ **Status: alpha — running in a real Kubernetes cluster as of 2026-05-17.** Validated against the live my.gaf.energy API on 2026-05-16 (real production data, JWT expiry tracking, clean halt on auth failure) and deployed via Helm to a kube-prometheus-stack cluster on 2026-05-17 (Prometheus scrapes confirmed by the [Grafana dashboard](examples/grafana-dashboard.json) built from real data). Still unverified in practice: Home Assistant `rest` sensor parsing `/api/state`, multi-property accounts, and the full ~25-day JWT lifetime. See [docs/setup.md](docs/setup.md#status) for the full validated-vs-untested breakdown.

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

The image is published to GHCR at `ghcr.io/dev-dull/gafferstape` — by default `docker compose up` pulls it, no local build needed. See [docs/setup.md](docs/setup.md) for the long-form walkthrough.

1. Clone the repo and `cp config.example.yaml config.yaml`. Paste your cookies into the `session_token` / `csrf_token` fields; the daemon hot-reloads them on every upstream call so future rotations don't require a container restart. (You can also use the `.env` path if you prefer — see [docs/setup.md §2](docs/setup.md#2-configure-gafferstape).)
2. Log in to [my.gaf.energy](https://my.gaf.energy) in Firefox, open dev-tools → Storage → Cookies → `wamy.gaf.energy`, copy `Session-Token` and `CSRF-Token`.
3. `docker compose up -d` (pulls the published image) — or `docker compose up -d --build` to build from this working tree instead.
4. Verify:
   ```
   curl http://localhost:9876/healthz       # {"ok":true}
   curl http://localhost:9876/api/state     # JSON snapshot
   curl http://localhost:9876/metrics       # Prometheus text
   ```
5. Point Prometheus at `http://localhost:9876/metrics`, or wire Home Assistant to `/api/state` using [examples/homeassistant.yaml](examples/homeassistant.yaml). Sample alert rules in [examples/alerts.yaml](examples/alerts.yaml). Ready-to-import Grafana dashboard in [examples/grafana-dashboard.json](examples/grafana-dashboard.json).

Cookies expire about every 25 days. Watch `gaf_session_expires_at_timestamp_seconds` (Grafana renders it as a date) or the `session_expires_at` field in `/api/state`, and edit `config.yaml` before they die.

## Kubernetes (Helm)

A Helm chart ships at [`charts/gafferstape/`](charts/gafferstape/) for cluster installs:

```sh
kubectl create secret generic gafferstape-cookies \
  --namespace gafferstape \
  --from-literal=session-token='eyJ...' \
  --from-literal=csrf-token='waHJ...'

helm install gafferstape ./charts/gafferstape \
  --namespace gafferstape --create-namespace \
  --set tokens.existingSecret=gafferstape-cookies
```

Full values reference, ServiceMonitor support, and rotation procedure: [charts/gafferstape/README.md](charts/gafferstape/README.md).

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
