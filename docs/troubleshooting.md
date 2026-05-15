# Troubleshooting

> Reminder: gafferstape has not been tested end-to-end against the live my.gaf.energy upstream with real cookies — see [setup.md](setup.md#status) for the full caveat. The symptoms and fixes below are best-effort based on the code, not field reports.

## `gaf_up` is 0, or `/api/state` says "authentication failed (status 401|403)"

Almost always: the cookies expired or were copied wrong.

1. **Did the JWT expire?** Check `gaf_session_expires_seconds`. If it's negative, the JWT is already dead. Re-run [setup §1](setup.md#1-capture-the-session-cookies), update `.env`, `docker compose up -d`.
2. **Cookies copied verbatim?** Firefox's Storage panel shows the cookie value in its single-URL-encoded form (`waHJbkZCfW%2bv...`). Copy that string exactly — don't decode `%2b` to `+` first. The daemon sends it as-is.
3. **Are you copying the Value column, not Name?** Sounds obvious, easy to miss.
4. **Reload after editing:**
   ```sh
   docker compose up -d
   docker compose logs --tail 20 gafferstape
   ```

After a real `AuthError` the daemon **halts the poller on purpose** — the upstream login is reCAPTCHA-gated, so blind retry just spam-fails. The HTTP server stays up so `/healthz` keeps working and `gaf_session_expires_seconds` keeps reporting.

## `/metrics` shows only Go runtime metrics, no `gaf_*` lines

The poller hasn't ticked yet. Immediately after startup `/metrics` exposes the standard `go_*` and `process_*` collectors but nothing else, because there's no snapshot to read from. Wait one `poll_interval` (default 15m) and check again — or watch the logs for the first `poll tick` debug line.

If you need to confirm the wiring faster, drop `poll_interval: 30s` into `config.yaml` temporarily. Revert it once you're satisfied; upstream only updates roughly hourly anyway.

## "Values look stale" — `today_kwh` hasn't moved

Upstream production data appears to update at roughly hourly granularity. Check `gaf_last_sample_timestamp_seconds` — if that's hours old, GAF's backend simply hasn't published a new bucket yet. Scraping Prometheus faster won't help; the daemon caches the poll result so scrapes are decoupled from upstream cadence.

## A property is missing from `/metrics`

By design: a property only appears when its `isReadyForEnergyProductionMonitoring` flag is true. Properties still provisioning, or with an inactive system, are skipped.

If a property *does* have monitoring enabled but isn't showing up, the per-property fetch failed for some other reason. Run:

```sh
docker compose logs gafferstape | grep -i property
```

You should see either a per-property error line or no mention at all (which means it was filtered out by the flag).

## Healthcheck fails / container is marked `unhealthy`

The container's healthcheck runs `/gaf-exporter --healthcheck`, which GETs its own `/healthz`. Failure scenarios:

- **Custom `listen`:** The healthcheck assumes `:9876`. If `config.yaml` sets a different listen, update the `healthcheck.test:` in `docker-compose.yaml` to match.
- **First scrape during start_period:** The 5s grace period is short. If your host is slow to start, the first probe may run before the server is bound. Compose will retry; it should settle within ~15s.
- **Stale image:** After editing the Dockerfile, `docker compose up -d --build` forces a rebuild. Without `--build` Compose reuses the cached image.

## "Daemon stopped polling but `/healthz` still works"

Intentional. On a 401/403 from upstream, the poller halts so it doesn't spam a dead session. You'll see one error line:

```
ERROR session is no longer authenticated; halting poller — paste fresh cookies and restart
```

Treat that as a paging condition. Refresh cookies, `docker compose up -d`, polling resumes.

## "I see a different error"

File an issue with:

- A redacted `docker compose logs gafferstape` excerpt — **strip any `Session-Token` strings**, they're live credentials.
- `curl http://localhost:9876/api/state` output.
- What you expected vs. observed.

https://github.com/dev-dull/gafferstape/issues
