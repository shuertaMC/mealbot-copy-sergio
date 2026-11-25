# Mealbot

Mealbot is one Go service that exposes the authenticated HTTP API, serves the
privacy page and sample CSV, runs scheduled pairings, and maintains pair
history in PostgreSQL.

## Requirements

- Go 1.27.4
- PostgreSQL 19.0

Run all commands from the repository root. Build the single deployable binary
from the module composition root:

```sh
go build -o mealbot ./cmd/mealbot
```

Do not put a developer username, database name, password, Auth0 value, or
Mailgun credential into Go or SQL files. Supply runtime configuration through
the environment. In particular, local database setup never requires editing
`db.go`.

## Configuration

The process reads one configuration snapshot at startup. Validation depends on
the selected command.

| Variable | Default | Used by | Requirement |
| --- | --- | --- | --- |
| `PORT` | `8080` | web | Optional TCP port override. |
| `DATABASE_URL` | none | web, `pair`, `migrate` | Required. Use a PostgreSQL URL such as `postgres://user:password@localhost:5432/mealbot?sslmode=disable`. |
| `AUTH0_ISSUER` | `https://mealbot.auth0.com/` | web, `pair` | Optional compatibility default; when overridden it must be an absolute URL. |
| `AUTH0_AUDIENCE` | `https://mealbot-2.herokuapp.com/` | web, `pair` | Optional compatibility default; when overridden it must be an absolute URI. |
| `AUTH0_JWKS_URL` | `https://mealbot.auth0.com/.well-known/jwks.json` | web, `pair` | Optional compatibility default; when overridden it must be an absolute URL. |
| `MAILGUN_DOMAIN` | none | `pair` | Required for notification delivery. |
| `MAILGUN_API_KEY` | none | `pair` | Required for notification delivery. |
| `MAILGUN_SMTP_LOGIN` | none | `pair` | Required sender identity for notification delivery. |
| `MEALBOT_TEST_MODE` | `false` | `pair` | Test-only opt-in. The value `true` (case-insensitive, surrounding whitespace ignored) skips Mailgun requirements and delivery; the database is still required. Never enable it for production pairing. |

The compatibility CORS policy is part of the startup configuration but is not
environment-selectable: it reflects the request origin, advertises
`Authorization, Content-Type, Origin, Accept, token`, and allows
`GET, POST, DELETE`.

Configuration errors name a missing or invalid setting without printing its
value. Unit tests construct configuration directly and do not need process
environment changes.

## Commands

```sh
# Start the HTTP server on $PORT, or 8080 when PORT is unset.
./mealbot

# Process all due, unfinished rounds. Mail is sent before a round is saved.
./mealbot pair

# Reconstruct last_round_with values from persisted pair history.
./mealbot migrate
```

Zero arguments select web mode. `pair` and `migrate` are the only recognized
single arguments; an unknown argument preserves the compatibility response
`argument '<value>' not recognized`. More than one argument preserves the
legacy behavior of selecting web mode.

Every valid mode opens `DATABASE_URL` and first applies the embedded numbered
SQL files in `migrations/`. The runner sorts by version, verifies stored
checksums in `schema_migrations`, serializes each migration with a PostgreSQL
advisory lock, and applies each outstanding file in its own transaction. It is
safe to repeat and non-destructively converges legacy `pair_counts` data into
`last_round_with`.

The public `mealbot migrate` command is deliberately separate: after schema
migrations are current, it rebuilds member pair history from existing pair
rows. `schema.sql` is only a compatibility bootstrap for a fresh database;
production and CI use the ordered embedded runner.

## Local setup

Create a dedicated PostgreSQL 19 database, set its URL, build, and run the
service:

```sh
export DATABASE_URL='postgres://mealbot:local-password@localhost:5432/mealbot?sslmode=disable'
go build -o mealbot ./cmd/mealbot
./mealbot
```

Startup applies the schema migrations. With the process running, the static
healthcheck and sample fixture are available without authentication:

```sh
curl --fail http://localhost:8080/privacy.html
curl --fail --output sample.csv http://localhost:8080/sample.csv
```

The seven API paths (`/members`, `/orgs`, `/org`, `/crossmatchtrait`,
`/rounds`, `/round`, and `/pairs`) require a valid configured Auth0 RS256 JWT.

## Tests and verification

Ordinary unit and handler tests do not contact PostgreSQL, Auth0, or Mailgun:

```sh
go test ./...
```

PostgreSQL integration tests are destructive inside temporary schemas and are
disabled unless explicitly opted in. Use an isolated PostgreSQL 19 database
whose name is exactly `test`, starts with `test_`, ends with `_test`, or ends
with `-test`. The guard rejects every other database name. Do not point the
integration variables at a development, staging, or production database.

```sh
export MEALBOT_RUN_POSTGRES_INTEGRATION=1
export MEALBOT_TEST_DATABASE_URL='postgres://mealbot_test:test-password@localhost:5432/mealbot_test?sslmode=disable'
go test ./...
```

`MEALBOT_TEST_DATABASE_URL` is preferred. Integration tests fall back to
`DATABASE_URL` only when the dedicated variable is empty, and the same safe
database-name guard still applies. Each test creates and drops its own schema.

CI uses Go 1.27.4 and PostgreSQL 19.0 to verify formatting, `go vet`, unit tests,
the opted-in integration suite, the race detector, `govulncheck`, a clean
module-cache download, and one shared `mealbot` binary in web, `pair`, and
`migrate` modes.

Dependencies resolve directly from the minimal Go module graph. The obsolete
`Godeps/` manifest and checked-in `vendor/` snapshot have been removed, so no
`GOFLAGS=-mod=mod` workaround is required.

## Deployment

The deployment contract remains one executable named `mealbot`. The exact
`Procfile` entry is:

```text
web: mealbot
```

Provision PostgreSQL 19, provide command-relevant environment variables, build
`./cmd/mealbot`, and start the zero-argument web process. Readiness can use
`GET /privacy.html`; it verifies the server and checked-in static asset without
requiring an Auth0 token. Schedule `mealbot pair` separately. Run
`mealbot migrate` during the rollout while deployed databases still require
pair-history reconstruction.

Third-party software and hosted-service distinctions are recorded in
`THIRD_PARTY_NOTICES.md`.
