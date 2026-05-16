# API notes: `my.gaf.energy`

What we know about the upstream API, derived from a HAR capture of a real login + browse session. None of this is documented publicly; it can change at any time without notice. Update this doc whenever something breaks.

> All sample values below use placeholders (`<PROPERTY_ID>`, `<EMAIL>`, etc). Don't commit the original HAR — it contains live session cookies, JWT, your Salesforce contact ID, and your email address.

## Hosts

- **API**: `https://wamy.gaf.energy`
- **Frontend**: `https://my.gaf.energy` (React SPA, not interesting on its own)

## Auth

Form-based login, cookie session, reCAPTCHA-gated.

### Login endpoint (informational — we don't call this from code)

```
POST https://wamy.gaf.energy/api/auth/login
Content-Type: application/json

{ "email": "...", "password": "...", "recaptchaToken": "..." }
```

Success response sets two cookies:

| Cookie | Format | Purpose |
|---|---|---|
| `Session-Token` | JWT (HS256), `exp` ~25 days out | Auth |
| `CSRF-Token` | URL-encoded opaque string | CSRF protection on state-changing requests |

The JWT issuer is `gafe-cp-be-prod`; payload includes `homeowner_id`, `homeowner_email`, `homeowner_contact_id` (Salesforce IDs), `login_type`, `exp`, `iss`, `aud`. We don't verify the signature — we only base64-decode to read `exp`.

### Why we don't automate login

reCAPTCHA v3 is mandatory on the login form. Automating it requires either a paid solver (2Captcha et al — costs money per solve, fragile against reCAPTCHA updates) or a headed browser the user interacts with anyway. Manual cookie paste every ~3 weeks is the least-bad option for a self-hosted tool.

## Endpoints we care about

### Solar production

```
GET /api/energy/get-production
  ?PropertyId=<PROPERTY_ID>
  &interval=hourly|daily|weekly|monthly|yearly
  &startTimestamp=<ISO8601 UTC>
  &endTimestamp=<ISO8601 UTC>
  &timezone=<IANA tz>
```

A `monthly` interval also exists (missed in the original Explore agent's notes). And a `timePeriod=lifetime` mode pairs with `interval=yearly` to return per-year totals since system install — `startTimestamp` is omitted in that mode. We don't currently expose lifetime mode in the Go client; the poller doesn't need it.

Response:

```json
{
  "startTimestamp": "2026-05-14T00:00:00",
  "endTimestamp":   "2026-05-14T23:43:00",
  "timePeriod":     "day",
  "interval":       "hourly",
  "unit":           "kWh",
  "timezone":       "America/Los_Angeles",
  "systemEnergy": [
    { "date": "2026-05-14T06:00:00", "value": 0.062 },
    { "date": "2026-05-14T07:00:00", "value": 0.144 }
  ]
}
```

Observations:
- `value` is the energy produced **during** that bucket, in kWh.
- `date` is in the requested timezone, naive (no offset).
- Buckets that haven't happened yet are simply absent — there are no nulls/zeros for the future.
- For `interval=hourly` you can ask for any time range; we ask for "today so far" each poll.

### Property list

```
GET /api/property/get-all
```

Returns an array of properties for the logged-in account: `id` (Salesforce, opaque), `streetAddress`, `timeZone`, `isReadyForEnergyProductionMonitoring`. We use this to discover property IDs on startup so the user doesn't have to paste them by hand.

### Account info (for inverter metadata)

```
GET /api/auth/get-account-info
```

Returns user profile plus inverter details: `manufacturer`, `model`, `serialNumber`, `isActive`. Useful as Prometheus labels so dashboards can distinguish multiple systems and surface hardware info.

## Request headers we send

- `Cookie: Session-Token=...; CSRF-Token=...` — both cookies on every request.
- `x-csrf-token: <CSRF-Token cookie value, verbatim>` — **confirmed** from the HAR: the portal sends this header on every authenticated GET (not just writes), and its value is the same as the `CSRF-Token` cookie. No decoding required — pass it through as-is. The cookie value as stored by Firefox is already URL-encoded for chars like `+`; we send the same string for both cookie and header. The wire-format double-encoding Firefox does on the `Cookie:` header (`%2b` → `%252b`) is a quirk of cookie transmission and doesn't seem to matter to the server.
- `User-Agent: gafferstape/<version> (+https://github.com/dev-dull/gafferstape)` — be findable. GAF can see who's hitting their API and click through to the project. The `+URL` form is the convention used by Googlebot etc.
- Standard `Accept: application/json`.

## Response envelope

**Every** endpoint (success or failure) returns this wrapper:

```json
{
  "data": <endpoint-specific payload, or null on failure>,
  "isSuccess": true,
  "errorMessage": null,
  "validationErrors": null,
  "traceId": "<32-hex>"
}
```

On `isSuccess: false`, `data` is null and `errorMessage` describes the problem. Always check `isSuccess` before reading `data`. The `traceId` is useful to quote when contacting GAF support.

## Response headers worth noting

- `x-csrf-token` — sometimes rotated; if we see a new value we should update our stored cookie. (Behavior to confirm.)
- `x-cloud-trace-context` — GCP tracing, indicates backend hosting. Ignore.
- No `RateLimit-*` headers observed, but that doesn't mean there's no limit — be conservative with poll frequency.

## Gotchas

- **No per-panel data.** The portal only exposes whole-system production. Per-microinverter telemetry isn't in any endpoint we've seen.
- **No real-time stream.** Updates appear roughly hourly; polling faster than ~15m gives you nothing new.
- **No battery state** in this dataset (the captured account doesn't have one — endpoint may exist for battery customers, untested).
- **Salesforce IDs are opaque.** `001Dp...` prefix is a Salesforce object key; treat as a string, don't try to parse it.
- **Timezone matters.** Production buckets are in property-local time. Always send the property's `timeZone` from `/property/get-all` rather than hardcoding.
- **Errors don't always carry useful bodies.** A 401 looks just like a 403 here; both mean "your session is dead, paste new cookies."
