# Refreshing client fixtures

The tests in this package use sanitized JSON fixtures under `testdata/`
to exercise the response decoders. When the upstream API changes shape,
those fixtures need refreshing. Here's how.

## 1. Capture a HAR

In Firefox: open `my.gaf.energy`, log in, click around to trigger every
call you want represented. Open dev-tools → Network → right-click an
entry → **Save All As HAR**. Save it _outside_ this repo. The HAR
contains a live JWT, real cookies, your email, and your Salesforce
IDs — it must never be committed. `*.har` is in `.gitignore` already,
but don't tempt fate.

## 2. Extract response bodies

```bash
HAR=path/to/your.har

jq -r '.log.entries[] | select(.request.method=="GET") |
  select(.request.url | contains("/api/property/get-all")) |
  .response.content.text' "$HAR" | jq . > /tmp/properties.json

jq -r '.log.entries[] | select(.request.method=="GET") |
  select(.request.url | contains("/api/auth/get-account-info")) |
  .response.content.text' "$HAR" | jq . > /tmp/account.json

# Hourly production for today
jq -r '.log.entries[] | select(.request.method=="GET") |
  select(.request.url | contains("interval=hourly")) |
  .response.content.text' "$HAR" | jq . > /tmp/production-hourly.json

# Daily production
jq -r '.log.entries[] | select(.request.method=="GET") |
  select(.request.url | contains("interval=daily")) |
  .response.content.text' "$HAR" | jq . > /tmp/production-daily.json
```

## 3. Sanitize

**Before** copying anything into `testdata/`, replace every real value
with the placeholder the tests expect:

| Field                                  | Replace with                      |
| -------------------------------------- | --------------------------------- |
| Property `id` (Salesforce 18-char)     | `PROP-FIXTURE-1`                  |
| Homeowner `id`                         | `HOMEOWNER-FIXTURE-1`             |
| Inverter `id`                          | `INVERTER-FIXTURE-1`              |
| `firstName`, `lastName`                | `Test`, `Homeowner`               |
| `email`                                | `homeowner@example.invalid`       |
| `phoneNumber`                          | `5550100`                         |
| `streetAddress`                        | `1 Example Way`                   |
| `city`, `state`, `zipCode`             | `Example City`, `OR`, `00000`     |
| Inverter `serialNumber`                | `FIXTURE-SERIAL-1`                |
| `traceId`                              | `fixture-trace-<endpoint>`        |
| `activatedDate`, `isEmailVerified`, …  | _safe-looking constants_          |

Energy values inside `systemEnergy` are not PII — leave them as captured
so the fixture exercises realistic decimal precision.

## 4. Move into place and run the tests

```bash
mv /tmp/properties.json        internal/client/testdata/properties.json
mv /tmp/account.json           internal/client/testdata/account.json
mv /tmp/production-hourly.json internal/client/testdata/production-hourly.json
mv /tmp/production-daily.json  internal/client/testdata/production-daily.json

go test ./internal/client/...
```

The tests assert against the sanitized placeholders above, so they will
fail loudly if you forgot to replace a value.

## 5. Double-check before committing

```bash
git diff --stat internal/client/testdata
git status
```

If `git status` mentions any `.har` file, stop — that means the HAR
landed inside the repo somehow. `.gitignore` should catch it, but
verify by hand before pushing.
