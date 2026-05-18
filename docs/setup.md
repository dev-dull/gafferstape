# Setup

<a id="status"></a>

> ## ⚠ Status: alpha — running in a real cluster as of 2026-05-17
>
> **Verified working:**
> - Builds and unit tests pass under the race detector.
> - The Docker image (~19 MB, multi-arch amd64/arm64) runs as a non-root user, reaches `healthy`, and shuts down cleanly on `SIGTERM`.
> - End-to-end smoke test against live `my.gaf.energy` with real cookies on 2026-05-16: `gaf_up=1`, one property auto-discovered, real `today_kwh` / `yesterday_kwh` / `latest_hour` values flowing, JWT expiry parsed correctly, inverter metadata (Delta M6-TL-US, serial, active flag) detected.
> - Authentication halts cleanly with `gaf_up=0` and a clear log line when cookies expire (verified with deliberately invalid cookies).
> - Token hot-reload: editing `config.yaml`'s `session_token` / `csrf_token` propagates to the next upstream call without a daemon restart.
> - The CSRF wire-encoding gotcha that prevented authentication on the first real test is fixed and locked in by a regression test.
> - **Helm chart deployed to a real Kubernetes cluster** with `kube-prometheus-stack` on 2026-05-17. That deploy also caught and fixed the `:v0.1.0` vs `:0.1.0` image-tag mismatch (#16).
> - **Prometheus actually scrapes `/metrics`** in that deployment — confirmed by the real-data Grafana dashboard shipped at [`examples/grafana-dashboard.json`](../examples/grafana-dashboard.json).
>
> **Not yet validated in practice:**
> - Home Assistant's `rest` sensor consuming `/api/state` in an actual HA install. The JSON shape is verified by unit tests and the schema works in Grafana, but no one has wired `examples/homeassistant.yaml` into a real HA configuration yet.
> - Multi-property accounts (only single-property tested).
>
> **Corrected after live testing (2026-05-18):**
> - The original docs assumed a ~25-day JWT lifetime (a bootstrap-time guess from cookie `Max-Age`). The GAF backend actually mints **~24h JWTs**, and tokens go stale roughly daily regardless of usage. v0.2.1 retuned alerts (WARN <6h, ERROR <1h) and dashboard thresholds. See [§7 Rotating cookies](#7-rotating-cookies) for the actual rotation cadence.
>
> If something doesn't match the description here, **that is a bug** — file it at https://github.com/dev-dull/gafferstape/issues with a redacted log excerpt and the output of `curl localhost:9876/api/state`.

## Prerequisites

- A GAF Energy account with at least one property that returns `isReadyForEnergyProductionMonitoring: true` in the portal. (You can verify by hitting `my.gaf.energy` and checking that you see production charts.)
- Docker and Docker Compose on the host that will run the daemon.
- A Prometheus server or Home Assistant install (or both) reachable from that host.
- Firefox for the cookie capture step. Other browsers work in principle — open their equivalent dev-tools storage panel — but the field names and paths below are Firefox's.

> **Deploying to Kubernetes instead?** [`charts/gafferstape/README.md`](../charts/gafferstape/README.md) is the K8s-equivalent of this guide. Same cookie-capture flow (§1 below), but `helm install` replaces `docker compose up`. Skip ahead to §1, capture cookies, then jump to the chart README.

## 1. Capture the session cookies

The portal protects its login form with reCAPTCHA, so the daemon can't sign in for you. Instead you log in once in a browser, copy the resulting cookies into a file, and re-do that roughly once a day — see [§7](#7-rotating-cookies) for the actual cadence and rotation procedure.

1. Open https://my.gaf.energy in Firefox and sign in.
2. Press `F12` (or `Cmd+Option+I` on macOS) to open dev-tools.
3. Switch to the **Storage** tab. If it isn't visible, click the `»` overflow at the right end of the tab bar.
4. In the left sidebar, expand **Cookies** and select `https://wamy.gaf.energy`.
5. Find these two rows and copy the **Value** column:
   - `Session-Token` — a long string beginning with `eyJ...`. It's a JWT signed by GAF's backend; the daemon decodes (without verifying) the `exp` claim to expose `gaf_session_expires_seconds`.
   - `CSRF-Token` — a shorter string usually ending in `=` or `%3d`. **Copy it exactly as Firefox shows it**; don't URL-decode anything.

Both cookies expire roughly **24 hours** after login. You'll repeat this step daily — see [§7](#7-rotating-cookies) for the rotation flow and why this is more frequent than the original docs claimed.

## 2. Configure gafferstape

```sh
git clone https://github.com/dev-dull/gafferstape.git
cd gafferstape
cp config.example.yaml config.yaml
cp .env.example .env
```

Pick one place to put the cookies. Both work, but they have different rotation properties:

**Option A — paste into `config.yaml` (recommended):**

```yaml
session_token: "eyJhbGciOi..."   # the full JWT
csrf_token: "waHJbkZCfW%2bv..."   # exactly as Firefox showed it
```

The daemon re-reads the file on every upstream request and picks up edits without restarting. `config.yaml` is gitignored, so cookies don't land in the repo.

**Option B — paste into `.env` (Docker Compose convenience):**

```sh
GAFFERSTAPE_SESSION_TOKEN=eyJhbGciOi...
GAFFERSTAPE_CSRF_TOKEN=waHJbkZCfW%2bv...
```

Env vars are baked into the container at start, so rotating means `docker compose up -d` to recreate. The env-var path is what to use when secrets are managed by an external tool (Docker secrets, Kubernetes, etc.) and you want them out of the mounted config file. If both file fields and env vars are set, the file fields win.

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
docker compose up -d           # pulls ghcr.io/dev-dull/gafferstape:latest
docker compose logs -f gafferstape
```

(Add `--build` to compile from the current working tree instead of pulling.)

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

## 5. Grafana

A ready-to-import dashboard ships at [`examples/grafana-dashboard.json`](../examples/grafana-dashboard.json). Grafana → **Dashboards** → **New** → **Import** → upload the JSON or paste its contents → pick your Prometheus data source when prompted. Twelve panels organised into four rows: status (session TTL, current generation gauge, UP/DOWN, last sample age, scrape error count), production (today vs yesterday vs 7-day average, current hour, peak today), history (full-width cumulative + hourly), and trends (weekly bar chart, 14-day production heatmap).

The dashboard was built against a real cluster on 2026-05-17 (commit ships with the JSON).

### Starter PromQL queries (fallback)

If you'd rather build panels manually, these are the highest-value queries:

For a panel of type "Time series" or "Stat":

| Display                                  | Query                                                  |
| ---------------------------------------- | ------------------------------------------------------ |
| Today's production so far (kWh)          | `gaf_energy_production_kwh_today`                      |
| Yesterday's total (stable until midnight)| `gaf_energy_production_kwh_yesterday`                  |
| Most recent hourly bucket                | `gaf_energy_production_kwh_latest_hour`               |
| Days until session cookies expire        | `gaf_session_expires_seconds / 86400`                  |
| When session cookies expire (panel-as-date) | `gaf_session_expires_at_timestamp_seconds` (Grafana panel format: "From Unix Time") |
| Exporter health                          | `gaf_up`                                               |

For multi-property accounts, every per-property metric carries `property_id` and `address` labels; break out by `property_id` to graph each system separately. Inverter make/model can be joined via `gaf_inverter_info`.

A ready-to-paste alerting bundle ships at [examples/alerts.yaml](../examples/alerts.yaml). Drop it into your Prometheus `rule_files:` directive. It defines four alerts: `GafferstapeDown` (polling unhealthy for 10m), `GafferstapeSessionExpiringSoon` (<6 hours — practical "rotate today" window for the ~24h JWT), `GafferstapeSessionExpired` (already dead), and `GafferstapeUpstreamErrors` (sustained churn after retries — points at a real problem the retry helper is masking).

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

## 7. Cookie lifetime and rotation

### How long the cookies last

Empirically, the GAF backend mints a JWT with **`exp ≈ login_time + 24 hours`** each time the user goes through the email/password form. There is no refresh-token endpoint we've found, and the JWT does not extend with use — once `exp` passes, the daemon halts polling and `gaf_up` drops to 0.

> The original bootstrap-time docs claimed ~25 days based on a cookie `Max-Age` attribute from the HAR. That was wrong: the cookie container would last 25 days in the browser, but the server stops honoring the JWT after about a day. Verified by decoding live tokens (`exp - now ≈ 20.74h` for a freshly-issued token, no `iat` claim) and by direct observation across multiple sessions. See [docs/api-notes.md §"What the JWT lifetime really is"](api-notes.md#what-the-jwt-lifetime-really-is-2026-05-18) for the full investigation.

This number can change at any time. Trust `gaf_session_expires_seconds` over any human assumption.

### Fresh-login best practice

The clock starts from when the user **logged in**, not from when they pasted the cookies. If Firefox has been holding a stale session for a few hours, the JWT may already be partway through its 24h lifetime by the time you copy it. To get the most out of each rotation:

1. **Sign out** of `my.gaf.energy` (kills the current session)
2. **Sign back in** with email + password
3. *Immediately* grab the cookies (Storage panel or Network tab, see [§1](#1-capture-the-session-cookies))
4. Paste into `config.yaml` (the daemon hot-reloads — no restart needed)

### What the daemon does as expiry approaches

| Window remaining | Daemon behaviour |
| --- | --- |
| > 6 hours | Silent. `gaf_session_expires_seconds` exposed for graphing. |
| 6 hours → 1 hour | `WARN session expires soon` on every poll (~96/day at default interval). |
| 1 hour → 0 | `ERROR session expires very soon` on every poll. |
| 0 (expired) | `ERROR session is no longer authenticated; halting poller`. `gaf_up=0`. The server keeps `/healthz` healthy so the container stays up and observable. |

[`examples/alerts.yaml`](../examples/alerts.yaml) fires `GafferstapeSessionExpiringSoon` at <6 hours — late enough that the average daily rotation doesn't trigger it spuriously, early enough to act before the daemon halts.

### Rotation command

**If your tokens live in `config.yaml` (Option A in [§2](#2-configure-gafferstape) — recommended):**

```sh
$EDITOR config.yaml   # paste the fresh values from a fresh login
# Done. The daemon picks up the new cookies on its next upstream call.
```

The container does not need to be restarted. The daemon stats the file on every API request and re-parses only when `mtime` changes, so the cost is essentially zero.

**If your tokens live in `.env` (Option B):**

```sh
$EDITOR .env
docker compose up -d   # recreates the container with the new env
```

**If you installed via Helm (v0.2.0+):**

```sh
kubectl create secret generic <your-secret-name> \
  --namespace gafferstape \
  --from-literal=session-token=NEW_JWT \
  --from-literal=csrf-token=NEW_CSRF \
  --dry-run=client -o yaml | kubectl apply -f -
```

That's it — no `kubectl rollout restart`. The chart mounts the Secret as files, the kubelet refreshes them within ~60s of your `kubectl apply`, and the daemon re-reads them on its next upstream request. For instant propagation, install [stakater/reloader](https://github.com/stakater/Reloader) and set `reloader.enabled=true`.

If you're on a pre-v0.2.0 chart, `helm upgrade` to the current chart version first; the older chart used env-injected Secrets and required `kubectl rollout restart` after every rotation.

Either way, no data loss — Prometheus and Home Assistant pick back up the moment polling resumes.

### Server-side policies we don't control

GAF's backend may invalidate sessions earlier than `exp` for any reason — password change on another device, prolonged inactivity, IP shift, manual revoke from a different browser. None of these are documented, and the daemon will simply start seeing 401s if it happens. The fix in every case is "sign in again, repaste."

## Found a bug?

File at https://github.com/dev-dull/gafferstape/issues with:

- The relevant `docker compose logs gafferstape` excerpt. **Redact any `Session-Token` strings before pasting** — they appear in URLs and headers and are valid credentials.
- The output of `curl http://localhost:9876/api/state`. The `ok`, `error`, and `session_expires_at` fields are usually enough.
- What you expected vs. what you got.
