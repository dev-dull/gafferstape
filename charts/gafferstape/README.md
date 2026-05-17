# gafferstape Helm chart

Kubernetes install of [gafferstape](https://github.com/dev-dull/gafferstape) — the Prometheus exporter + Home Assistant JSON endpoint for GAF Energy solar production data.

The Docker Compose flow is still the documented happy path for single-host self-hosters; this chart is the install option for clusters.

> Status: alpha — chart deployed to a real `kube-prometheus-stack` cluster on 2026-05-17. That deploy caught the `:v0.1.0` vs `:0.1.0` image tag mismatch ([#16](https://github.com/dev-dull/gafferstape/issues/16), since fixed). Prometheus scraping + Grafana dashboard confirmed working against real data. The [project status](../../docs/setup.md#status) caveat applies for the items still unverified (Home Assistant integration, multi-property accounts, ~25-day JWT lifetime).

## TL;DR

```sh
kubectl create namespace gafferstape

# 1. Capture fresh cookies from a Firefox login (see ../../docs/setup.md §1).
# 2. Store them in a Secret managed outside Helm:
kubectl create secret generic gafferstape-cookies \
  --namespace gafferstape \
  --from-literal=session-token='eyJ...' \
  --from-literal=csrf-token='waHJbkZCfW%2bv...'

# 3. Install the chart referencing that Secret:
helm install gafferstape ./charts/gafferstape \
  --namespace gafferstape \
  --set tokens.existingSecret=gafferstape-cookies
```

That's it. The Deployment comes up, `/healthz` passes, and the daemon starts polling on its 15-minute interval.

## Values

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `image.repository` | string | `ghcr.io/dev-dull/gafferstape` | Image registry + repository |
| `image.tag` | string | `""` (chart appVersion) | Image tag override |
| `image.pullPolicy` | string | `IfNotPresent` | Pull policy |
| `imagePullSecrets` | list | `[]` | Image pull Secret references |
| `replicaCount` | int | `1` | Pod replicas. **Keep at 1** — cookies are single-session |
| `serviceAccount.create` | bool | `true` | Create a dedicated ServiceAccount |
| `serviceAccount.name` | string | `""` | ServiceAccount name override |
| `serviceAccount.annotations` | map | `{}` | Annotations on the ServiceAccount |
| `podAnnotations` | map | `{}` | Extra pod annotations |
| `podSecurityContext` | map | nonroot 65532 + seccomp RuntimeDefault | Pod-level security context |
| `securityContext` | map | drop ALL caps, readOnlyRootFS, no privesc | Container security context |
| `resources.requests.cpu` | string | `50m` | CPU request |
| `resources.requests.memory` | string | `64Mi` | Memory request |
| `resources.limits.cpu` | string | `200m` | CPU limit |
| `resources.limits.memory` | string | `128Mi` | Memory limit |
| `nodeSelector` | map | `{}` | Node selector |
| `tolerations` | list | `[]` | Tolerations |
| `affinity` | map | `{}` | Affinity rules |
| `service.type` | string | `ClusterIP` | Service type |
| `service.port` | int | `9876` | Service port |
| `serviceMonitor.enabled` | bool | `false` | Create a `monitoring.coreos.com/v1` ServiceMonitor (requires Prometheus Operator CRDs) |
| `serviceMonitor.interval` | string | `30s` | Scrape interval |
| `serviceMonitor.scrapeTimeout` | string | `10s` | Scrape timeout |
| `serviceMonitor.labels` | map | `{}` | Extra labels (e.g. `release: kube-prometheus-stack`) |
| `config.listen` | string | `":9876"` | Daemon listen address |
| `config.pollInterval` | string | `"15m"` | How often to hit the GAF API |
| `config.logLevel` | string | `"info"` | `debug`/`info`/`warn`/`error` |
| `config.properties` | list | `[]` (auto-discover) | Pin to specific Salesforce property IDs |
| `tokens.create` | bool | `false` | Render a Secret from `tokens.session` / `tokens.csrf` (dev only — values land in Helm release history) |
| `tokens.session` | string | `""` | Session-Token JWT value, used only when `tokens.create=true` |
| `tokens.csrf` | string | `""` | CSRF-Token value, used only when `tokens.create=true` |
| `tokens.existingSecret` | string | `""` | Name of an externally-managed Secret with keys `session-token` and `csrf-token` (recommended for prod) |
| `reloader.enabled` | bool | `false` | Add `reloader.stakater.com/auto` annotation so [stakater/reloader](https://github.com/stakater/Reloader) auto-rolls the Deployment on Secret/ConfigMap changes |

## Common configurations

### Production: external Secret + ServiceMonitor

```yaml
# values-prod.yaml
tokens:
  existingSecret: gafferstape-cookies

serviceMonitor:
  enabled: true
  labels:
    release: kube-prometheus-stack    # match your Prometheus Operator selector

resources:
  limits:
    cpu: 250m
    memory: 192Mi
```

```sh
helm upgrade --install gafferstape ./charts/gafferstape \
  --namespace gafferstape --create-namespace \
  -f values-prod.yaml
```

### Dev / kind cluster: chart-rendered Secret

```sh
helm install gaf ./charts/gafferstape \
  --namespace gaf --create-namespace \
  --set tokens.create=true \
  --set tokens.session='eyJ...' \
  --set tokens.csrf='waHJ...'
```

> Note: the cookie values end up in the Helm release history; rotate them before treating this install as anything other than throwaway.

### Grafana dashboard

Once `serviceMonitor.enabled=true` and Prometheus is scraping, import [`examples/grafana-dashboard.json`](../../examples/grafana-dashboard.json) into Grafana via **Dashboards → Import**. Twelve panels, including the session-expiry countdown and a 14-day production heatmap. Built and validated against a real `kube-prometheus-stack` deployment.

### Auto-rollout on Secret update via reloader

```sh
helm upgrade gafferstape ./charts/gafferstape \
  --reuse-values --set reloader.enabled=true
```

Requires [stakater/reloader](https://github.com/stakater/Reloader) installed cluster-wide. With it, updating the cookies Secret triggers a rollout automatically.

## Cookie rotation

The session JWT expires roughly 25 days after login. Watch `gaf_session_expires_at_timestamp_seconds` (Prometheus) or the `session_expires_at` field in `/api/state` (Home Assistant) and rotate before it dies. The Prometheus alert `GafferstapeSessionExpiringSoon` in [examples/alerts.yaml](../../examples/alerts.yaml) fires 3 days out.

### Rotation procedure

```sh
# 1. Capture fresh values from a Firefox login at my.gaf.energy
#    — see ../../docs/setup.md §1.

# 2. Update the Secret:
kubectl create secret generic gafferstape-cookies \
  --namespace gafferstape \
  --from-literal=session-token='NEW_JWT' \
  --from-literal=csrf-token='NEW_CSRF' \
  --dry-run=client -o yaml | kubectl apply -f -

# 3. Restart the Deployment so the new env vars are picked up:
kubectl rollout restart deployment/gafferstape \
  --namespace gafferstape
```

If `reloader.enabled=true` and stakater/reloader is installed, step 3 is automatic.

## Uninstall

```sh
helm uninstall gafferstape --namespace gafferstape
kubectl delete secret gafferstape-cookies --namespace gafferstape   # only if you created it
kubectl delete namespace gafferstape                                 # if empty
```

## Limitations

- **No HA / multi-replica.** Cookies are single-session credentials. Running >1 replica means N pods making duplicate requests with the same auth — wasteful, possibly rate-limit-triggering. `replicaCount` defaults to 1 for this reason.
- **Token rotation requires a Pod restart** (or stakater/reloader). The daemon supports in-place hot-reload of `config.yaml` token fields, but in Kubernetes the cookies live in a Secret mounted as env vars, which the kubelet only re-reads at container start. The `checksum/config` annotation on the Pod handles ConfigMap edits (poll interval, log level, etc.); Secret changes need the rollout.
- **No CRDs bundled.** ServiceMonitor support requires the Prometheus Operator CRDs to already be installed (typically via kube-prometheus-stack).

## Validation in CI

The chart is `helm lint`'d and `helm template`'d against three value combinations (defaults, existingSecret, serviceMonitor) on every push to main and every PR — see [.github/workflows/ci.yaml](../../.github/workflows/ci.yaml). Rendered manifests are then schema-validated with `kubeconform`.

What CI does **not** cover: an actual `kubectl apply` to a live cluster. The first real deployment will be the first proof.
