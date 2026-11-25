# Mealbot

Mealbot is one Go 1.26 service that imports organization members, schedules rounds, creates deterministic pairings, emails each pair or triple, and exposes pairing history through an authenticated HTTP API. PostgreSQL stores all state; Auth0-compatible RS256 JWTs protect API routes; Mailgun delivers messages. Static privacy and sample CSV files remain public.

## Architecture

- `cmd/mealbot`: the only executable and process entry point. With no argument it serves HTTP; `pair` processes due rounds; `migrate` rebuilds pairing history.
- `internal/app`: configuration, dependency composition, command selection, and graceful server shutdown.
- `internal/httpapi`: routes, request limits, response/error mapping, CORS, CSV parsing, and consumer-owned use-case interfaces.
- `internal/domain`: validated organization, member, round, pair, and typed error models.
- `internal/pairing`: deterministic pairing engine and the deliver-before-persist scheduler.
- `internal/auth`: RS256 validation plus timeout-bound, cached JWKS retrieval.
- `internal/mailer`: provider-neutral message composition and the Mailgun v4 adapter.
- `internal/store/postgres`: context-aware transactional repositories and history reconstruction.
- `migrations`: ordered SQL embedded into the service and recorded in `schema_migrations`.
- `static`: public `privacy.html` and `sample.csv` assets.

The PostgreSQL, JWT, and mail dependencies are injected behind narrow consumer interfaces. Unit and smoke tests use local fakes and never contact live providers.

## Requirements and safe local setup

Install Go 1.26 (the lifecycle uses Go 1.26.11) and PostgreSQL 17. Clone the repository, then download and normalize dependencies:

```sh
go mod download
go mod tidy
go build -o mealbot ./cmd/mealbot
```

Use a dedicated local database and a non-privileged database role. Select exactly one database configuration:

```sh
export APP_ENV=development
export DB_USER=mealbot_local
export DB_NAME=mealbot_local
export STATIC_DIR=static
```

or:

```sh
export APP_ENV=development
export DATABASE_URL='postgresql://mealbot_local@127.0.0.1:5432/mealbot_local?sslmode=disable'
export STATIC_DIR=static
```

Do not set `DATABASE_URL` together with `DB_USER` or `DB_NAME`. Local `DB_USER`/`DB_NAME` connections use the platform PostgreSQL defaults; use `DATABASE_URL` when host, port, password, TLS, or other connection options are needed.

The service applies embedded forward migrations at startup. For a `psql`-based local bootstrap, `schema.sql` includes the same three ordered migration files and is non-destructive:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f schema.sql
```

Never run `internal/store/postgres/testdata/reset.sql` against retained data. It drops application tables and exists only for disposable integration databases whose name contains `test`. Back up production data and test migrations on a copy before deployment.

Development mode allows absent provider settings so configuration and static behavior can be tested with fakes, but real authenticated HTTP startup still requires valid Auth0 settings and `pair` requires valid Mailgun settings.

## Configuration

Configuration is read once at process startup. Durations use Go syntax such as `500ms`, `5s`, or `2m`.

| Variable | Default | Contract |
| --- | --- | --- |
| `APP_ENV` | `development` | `development` or `production`. Production requires all Auth0 and Mailgun values. |
| `PORT` | `8080` | TCP port from 1 through 65535. |
| `DATABASE_URL` | none | Absolute `postgres://` or `postgresql://` URL; mutually exclusive with local database fields. |
| `DB_USER` | none | Local PostgreSQL role; required with `DB_NAME` when `DATABASE_URL` is absent. |
| `DB_NAME` | none | Local PostgreSQL database; required with `DB_USER` when `DATABASE_URL` is absent. |
| `AUTH0_ISSUER` | none | Absolute HTTP(S) JWT issuer URL, normally ending in `/`. |
| `AUTH0_AUDIENCE` | none | Required JWT audience; string and array audience claims are accepted. |
| `AUTH0_JWKS_URL` | none | Absolute HTTP(S) JWKS endpoint used for RS256 keys. |
| `MAILGUN_SMTP_LOGIN` | none | Sender mailbox placed in the organization-derived `From` header. |
| `MAILGUN_DOMAIN` | none | Mailgun sending domain. |
| `MAILGUN_API_KEY` | none | Mailgun API secret. |
| `STATIC_DIR` | `static` | Existing directory served without authentication. |
| `CORS_ALLOWED_ORIGINS` | empty | Comma-separated exact origins, for example `https://app.example`; no wildcard or path is allowed. |
| `REQUEST_TIMEOUT` | `15s` | Inbound request plus server read/write timeout. |
| `JWKS_TIMEOUT` | `5s` | Maximum duration of one JWKS retrieval. |
| `MAIL_TIMEOUT` | `10s` | Maximum duration of one Mailgun delivery. |

Production secrets must come from the deployment secret store, must never be committed or printed, and should use least-privilege credentials with a rotation policy. Use HTTPS Auth0 endpoints, TLS-verified PostgreSQL connections, and separate development/test/production databases and provider accounts.

## Run and deploy

Build and start the HTTP service:

```sh
go build -o mealbot ./cmd/mealbot
./mealbot
```

With the default `PORT=8080`, verify that the public static privacy page is served without authentication:

```sh
curl --fail --silent --show-error http://127.0.0.1:8080/privacy.html | grep -F '<title>Privacy Policy</title>'
```

Use the configured port in the URL when `PORT` is not `8080`. A successful command proves both an HTTP 2xx response and the expected privacy-page content.

Process due rounds or reconstruct all organizations' pair history:

```sh
./mealbot pair
./mealbot migrate
```

`pair` sends every group message before atomically persisting pair rows, symmetric histories, and round completion. If any send fails, database state is unchanged, although an earlier provider-accepted message cannot be recalled. `migrate` is idempotent and includes inactive historical members; a dangling member reference is an integrity error.

`Procfile` contains one `web: mealbot` process. Deployment must build `./cmd/mealbot` as the `mealbot` binary (equivalent to `go build -o mealbot ./cmd/mealbot`) and place it on the process `PATH`.

## API contract

All API routes require `Authorization: Bearer <RS256-token>`. Static routes do not. A valid CORS preflight is handled before authentication. Query keys must occur exactly once and contain a non-blank value. Unsupported methods return JSON `405`; malformed requests return JSON `400`; missing/invalid credentials return JSON `401`; disallowed origins return JSON `403`; internal failures return a stable JSON `500` without provider or database details.

| Method and route | Request | Success |
| --- | --- | --- |
| `GET /members?org=<name>` | Exact `org` query. | `200` JSON `{members, traits, crossMatchTrait}`. |
| `POST /members?org=<name>` | `multipart/form-data`, exactly one file field named `members`; bounded by 32 MiB. CSV requires unique case/space-normalized `name` and `email` headers; other columns are metadata. | `201` JSON `{members, traits}` after atomic reconciliation. |
| `GET /orgs?admin=<id>` | Exact `admin` query. | `200` JSON `{orgs:[...]}`. |
| `POST /org?admin=<id>` | JSON `{"org":"organization"}` with no unknown/trailing fields. | `201` text `Successfully created new organization`. |
| `POST /crossmatchtrait?org=<name>` | JSON `{"trait":"metadata-key"}`; JSON `null` clears it. | `201` text `Successfully set the cross match trait`. |
| `GET /rounds?org=<name>` | Exact `org` query. | `200` JSON `{rounds:[...]}` in ID order. |
| `POST /round?org=<name>&round=<timestamp>` | RFC3339 or supported legacy timestamp. | `201` text scheduling confirmation. |
| `POST /round?org=<name>&round=<timestamp>&roundId=<id>` | Adds exact non-negative `roundId` to reschedule. | `201` text reschedule confirmation. |
| `DELETE /round?org=<name>&roundId=<id>` | Exact non-negative `roundId`; deletion compacts later IDs and histories transactionally. | `201` text removal confirmation. |
| `GET /pairs?org=<name>` | Exact `org` query. | `200` round-grouped JSON `{roundPairs:[...]}`, including inactive historical members. |
| `GET /privacy.html` | Public static request. | Privacy page. |
| `GET /sample.csv` | Public static request. | Named CSV import example. |

CSV imports reject empty/header-only data, duplicate emails (case-insensitive), duplicate/missing/blank/reserved headers, unequal row widths, blank names/emails, malformed quoting, invalid email addresses, and oversized bodies. A successful import updates names and metadata, reactivates present members, deactivates omitted members, and preserves/extends symmetric history in one transaction.

## Verification workflows

The managed project lifecycle runs these as separate gates: `install`, `format_check`, `build`, `smoke`, `unit_test`, `race_test`, `coverage`, `vet`, `govulncheck`, and the opt-in `postgres_integration`. Their repository equivalents are:

```sh
go mod download && go mod tidy && go mod tidy -diff
test -z "$(gofmt -l cmd internal migrations)"
go build -o mealbot ./cmd/mealbot
go test -count=1 ./...
go test -race -count=1 ./...
go test -covermode=atomic -coverprofile=coverage.out ./internal/...
go tool cover -func=coverage.out
go vet ./...
govulncheck ./...
```

Internal aggregate statement coverage must remain at least 80%. Fast tests require no network, provider credentials, PostgreSQL, writable repository, or execution order. The authenticated organization/static smoke test and fake-backed `pair`, `migrate`, invalid-argument, and default-server lifecycle tests are part of the normal suite.

To run the PostgreSQL suite, point only at an empty disposable PostgreSQL 17 database whose name contains `test`:

```sh
export RUN_POSTGRES_INTEGRATION=1
export DATABASE_URL='postgresql://mealbot_test@127.0.0.1:5432/mealbot_test?sslmode=disable'
go test -count=1 -tags=integration ./...
```

The integration suite resets that database, migrates from empty state, and exercises constraints, JSONB, reconciliation, rollback, round compaction, pair/triple history, due claims, and idempotent reconstruction. Auth tests generate local RSA keys and serve local JWKS responses; mail tests use an injected API and never call Mailgun.

The pre-refactor `go test ./...` result captured before behavior moved into the modular packages is preserved in [`docs/baseline-test-result.md`](docs/baseline-test-result.md).

## Dependency compatibility and security

The direct `require` declarations retain the milestone compatibility baselines: `github.com/mailgun/mailgun-go/v4` v4.21.1 and `github.com/jackc/pgx/v5` v5.7.6. They are declarations, not the code compiled into Mealbot. Version-scoped same-module `replace` directives make the effective build graph use Mailgun v4.21.0 and pgx v5.13.0 respectively.

Mailgun v4.21.1 was never published, so v4.21.0 is the nearest resolvable v4.21 implementation. pgx v5.7.6 has reachable [GO-2026-5004](https://pkg.go.dev/vuln/GO-2026-5004); the effective v5.13.0 implementation is newer than the v5.9.2 fixed version. The graph also explicitly selects `github.com/go-chi/chi/v5` v5.5.0 instead of Mailgun's older transitive v5.1.0, because chi's [GO-2025-3770](https://pkg.go.dev/vuln/GO-2025-3770) is fixed from v5.2.2 onward, and retains `golang.org/x/text` v0.44.0. Do not remove these replacements or downgrade the effective versions merely to make the declarations and implementations identical; `go list -m all` reports each declared baseline followed by its effective replacement.

## External services

- PostgreSQL 17 is the only datastore. Runtime access uses `database/sql` with `pgx/v5/stdlib`; there is no ORM.
- Auth0 (or a compatible issuer) must publish RSA signing keys at the configured JWKS URL. Keys are cached and refreshed on an unknown `kid`; all fetches are bounded.
- Mailgun v4 sends plain-text group messages. Recipients, sender layout, subject, introduction, names, and footer preserve the service contract.

See `docs/origin-symbol-traceability.md` for the exhaustive legacy-to-modular symbol and test mapping.
