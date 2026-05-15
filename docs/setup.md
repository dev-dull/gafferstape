# Setup

<a id="status"></a>

> ## ⚠ Status: not yet tested end-to-end
>
> **What has been verified:**
> - The Go code builds and passes its unit tests under the race detector.
> - The Docker image builds (~19 MB), runs as a non-root user, and reaches a healthy state.
> - The daemon correctly halts polling and surfaces the failure when given invalid cookies against the real my.gaf.energy upstream.
>
> **What has NOT been verified:**
> - Successful authentication with real cookies against the live API. Every smoke test so far has used garbage tokens that produce a real 401.
> - Prometheus, Grafana, or Home Assistant actually consuming the output. The metric names, JSON shape, and HA YAML are designed correctly on paper but unproven in practice.
> - Long-running behaviour over the full ~25-day JWT lifetime.
> - Whether the cookie-extraction steps below match the latest Firefox UI exactly. They're derived from inspecting a HAR capture from one session.
>
> If something doesn't match the description here, **that is a bug** — file it at https://github.com/dev-dull/gafferstape/issues with a redacted log excerpt and the output of `curl localhost:9876/api/state`.

## Prerequisites

- A GAF Energy account with at least one property that returns `isReadyForEnergyProductionMonitoring: true` in the portal. (You can verify by hitting `my.gaf.energy` and checking that you see production charts.)
- Docker and Docker Compose on the host that will run the daemon.
- A Prometheus server or Home Assistant install (or both) reachable from that host.
- Firefox for the cookie capture step. Other browsers work in principle — open their equivalent dev-tools storage panel — but the field names and paths below are Firefox's.

## 1. Capture the session cookies

The portal protects its login form with reCAPTCHA, so the daemon can't sign in for you. Instead you log in once in a browser, copy the resulting cookies into a file, and rotate them every few weeks.

1. Open https://my.gaf.energy in Firefox and sign in.
2. Press `F12` (or `Cmd+Option+I` on macOS) to open dev-tools.
3. Switch to the **Storage** tab. If it isn't visible, click the `»` overflow at the right end of the tab bar.
4. In the left sidebar, expand **Cookies** and select `https://wamy.gaf.energy`.
5. Find these two rows and copy the **Value** column:
   - `Session-Token` — a long string beginning with `eyJ...`. It's a JWT signed by GAF's backend; the daemon decodes (without verifying) the `exp` claim to expose `gaf_session_expires_seconds`.
   - `CSRF-Token` — a shorter string usually ending in `=` or `%3d`. **Copy it exactly as Firefox shows it**; don't URL-decode anything.

Both cookies expire roughly 25 days after login. You'll repeat this step every few weeks — see [§7](#7-rotating-cookies).

## 2. Configure gafferstape

```sh
git clone https://github.com/dev-dull/gafferstape.git
cd gafferstape
cp config.example.yaml config.yaml
cp .env.example .env
```

Edit `.env` and paste the cookies from step 1:

```sh
GAFFERSTAPE_SESSION_TOKEN=eyJhbGciOi...     # the full JWT, no quotes needed
GAFFERSTAPE_CSRF_TOKEN=waHJbkZCfW%2bv...    # exactly as Firefox showed it
```

You shouldn't usually need to touch `config.yaml` — the defaults are sensible:

| Field          | Default            | When to change                                                                    |
| -------------- | ------------------ | --------------------------------------------------------------------------------- |
| `listen`       | `:9876`            | If something else on the host already uses that port.                             |
| `poll_interval`| `15m`              | Upstream updates roughly hourly; smaller values just burn requests for nothing.   |
| `log_level`    | `info`             | `debug` while you're chasing a problem; `warn` for steady-state.                  |
| `properties`   | `[]` (auto-discover) | Set to a list of property IDs if your account has properties you don't want polled. |

Leave `session_token` and `csrf_token` blank in `config.yaml`. The env vars in `.env` override them, which keeps the secret-bearing values out of the file that mounts into the container.

## 3. Run it

```sh
docker compose up -d --build
docker compose logs -f gafferstape
```

You should see, in order, something like:

```
{"level":"INFO","msg":"starting","version":"dev","listen":":9876","poll_interval":"15m0s",...}
{"level":"INFO","msg":"listening","addr":":9876"}
{"level":"DEBUG","msg":"poll tick","fetched":1,"elapsed":"..."}
```

Verify the endpoints from the host:

```sh
curl http://localhost:9876/healthz       # {"ok":true}
curl http://localhost:9876/api/state     # JSON with "ok":true and your property
curl http://localhost:9876/metrics | grep '^gaf_'
```

If `/api/state` shows `"ok":false` with `"authentication failed (status 401)"`, the cookies didn't take. See [troubleshooting.md](troubleshooting.md#gaf_up-is-0-or-authentication-failed-status-401403).

## 4. Wire up Prometheus

Add this to your Prometheus `scrape_configs`:

```yaml
scrape_configs:
  - job_name: gafferstape
    scrape_interval: 30s
    static_configs:
      - targets:
          - gafferstape:9876   # or whatever resolves to the container
```

`30s` is fine — the daemon caches its poll result, so scraping faster than the upstream cadence costs nothing real but slightly noisier dashboards. Don't go below the default 15s; it doesn't buy anything.

## 5. Grafana / PromQL starter queries

For a panel of type "Time series" or "Stat":

| Display                                  | Query                                       |
| ---------------------------------------- | ------------------------------------------- |
| Today's production so far (kWh)          | `gaf_energy_production_kwh_today`           |
| Yesterday's total (stable until midnight)| `gaf_energy_production_kwh_yesterday`       |
| Most recent hourly bucket                | `gaf_energy_production_kwh_latest_hour`    |
| Days until session cookies expire        | `gaf_session_expires_seconds / 86400`       |
| Exporter health                          | `gaf_up`                                    |

For multi-property accounts, every per-property metric carries `property_id` and `address` labels; break out by `property_id` to graph each system separately. Inverter make/model can be joined via `gaf_inverter_info`.

A starter alerting bundle (drop into your existing rules file; a polished version lands with [#8](https://github.com/dev-dull/gafferstape/issues/8)):

```yaml
groups:
  - name: gafferstape
    rules:
      - alert: GafferstapeSessionExpiringSoon
        expr: gaf_session_expires_seconds < 86400 * 3
        labels: { severity: warning }
        annotations:
          summary: "Gafferstape cookies expire in <3 days"
          description: "Refresh per docs/setup.md and redeploy."

      - alert: GafferstapeDown
        expr: gaf_up == 0
        for: 10m
        labels: { severity: warning }
        annotations:
          summary: "Gafferstape can't fetch from my.gaf.energy"
```

## 6. Wire up Home Assistant

The daemon serves the same data as JSON at `/api/state` for HA's built-in `rest` sensor. A ready-to-paste config lives at [examples/homeassistant.yaml](../examples/homeassistant.yaml) — drop it into your HA `configuration.yaml`, or `!include` it from there:

```yaml
# configuration.yaml
rest: !include gafferstape.yaml
```

It exposes three sensors:

- `sensor.solar_production_today`
- `sensor.solar_production_latest_hour`
- `sensor.solar_production_yesterday`

All three are marked `unavailable` when the daemon's `ok` field is false (e.g. during cookie expiry), so HA's history graphs gap honestly instead of charting a flat zero.

## 7. Rotating cookies

About every three weeks you'll need to repeat [§1](#1-capture-the-session-cookies). Then:

```sh
$EDITOR .env                  # paste the fresh values
docker compose up -d          # recreates the container with the new env
```

The `GafferstapeSessionExpiringSoon` alert from [§5](#5-grafana--promql-starter-queries) should fire 3 days before the JWT dies, so the rotation isn't a surprise.

## Found a bug?

File at https://github.com/dev-dull/gafferstape/issues with:

- The relevant `docker compose logs gafferstape` excerpt. **Redact any `Session-Token` strings before pasting** — they appear in URLs and headers and are valid credentials.
- The output of `curl http://localhost:9876/api/state`. The `ok`, `error`, and `session_expires_at` fields are usually enough.
- What you expected vs. what you got.
