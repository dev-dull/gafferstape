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

## Kubernetes-specific

### Pod is `CrashLoopBackOff` immediately after install

Almost always a Secret problem.

1. Confirm the Secret exists with the **exact** key names `session-token` and `csrf-token`:
   ```sh
   kubectl describe secret <your-secret-name> --namespace <ns>
   # Data
   # ====
   # csrf-token:     XXX bytes
   # session-token:  XXX bytes
   ```
   If the keys are `sessionToken` / `csrfToken` (camelCase) or `SESSION_TOKEN` / `CSRF_TOKEN`, the env var lookup fails and the daemon exits.
2. Confirm the chart references the right Secret. If you used `tokens.existingSecret=foo`, the Deployment's env vars should reference `name: foo`:
   ```sh
   kubectl get deployment <release>-gafferstape -n <ns> -o jsonpath='{.spec.template.spec.containers[0].env}'
   ```
3. `kubectl logs <pod>` will usually give the actual error (env var missing, parse failure, etc).

### ServiceMonitor not picked up by Prometheus

The Prometheus Operator only selects ServiceMonitors with labels matching its `serviceMonitorSelector`. kube-prometheus-stack defaults to `release: <helm-release-name>`. Set `serviceMonitor.labels.release: kube-prometheus-stack` (or whatever your operator release is named) in chart values.

```sh
kubectl get prometheus -A -o jsonpath='{.items[*].spec.serviceMonitorSelector}'
```

shows the active selector. The chart's ServiceMonitor needs labels matching it.

### Cookies updated but pod is still failing auth

Since v0.2.0 the chart mounts the Secret as files (not env vars), so updates propagate within the kubelet sync interval (~60s) without a Pod restart. If you're still seeing auth failures after that window:

1. **Confirm the new Secret values landed.** `kubectl get secret <name> -n <ns> -o jsonpath='{.data.session-token}' | base64 -d | head -c 60` — should be the JWT prefix of your fresh token.
2. **Confirm the Pod sees them.** `kubectl exec -n <ns> deploy/<release>-gafferstape -- head -c 60 /etc/gafferstape-secrets/session-token` — should match. If the file is missing entirely, the chart wasn't installed at v0.2.0+ or the Secret's key names don't match (`session-token` and `csrf-token`).
3. **Wait one poll interval.** Default 15m. `/api/state`'s `session_expires_at` is the ground truth.
4. **If still wrong after step 3:** rule out the pre-v0.2.0 env-var path with `kubectl rollout restart deployment/<release>-gafferstape -n <ns>` — if that fixes it, you're on an older chart and need to `helm upgrade`.

For instant propagation rather than waiting up to ~60s, install [stakater/reloader](https://github.com/stakater/Reloader) and set `reloader.enabled=true` in chart values.

### Pod evicted / OOMKilled

The default `resources.limits.memory: 128Mi` is conservative. If your account has many properties or you bumped `poll_interval` very low, watch `kubectl top pod` for a few hours and bump the limit. The Go runtime overhead floor is ~40Mi; everything else is the property snapshot cache (small) and the Prometheus scrape buffer.

## "I see a different error"

File an issue with:

- A redacted log excerpt (`docker compose logs gafferstape` or `kubectl logs <pod>`) — **strip any `Session-Token` strings**, they're live credentials.
- `curl <endpoint>/api/state` output.
- What you expected vs. observed.

https://github.com/dev-dull/gafferstape/issues
