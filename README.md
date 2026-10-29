# Mealbot

Mealbot pairs up the members of an organization for regular meals. It is a Go
service with a REST API (Postgres for storage, Auth0 for authentication) and a
batch job that runs the scheduled pairing rounds and emails the groups via Mailgun.

# Intro (for people new to Go)
- Check out [A Tour of Go](https://go.dev/tour/welcome/1) to learn Go syntax
- Download Go (1.24 or newer) from [go.dev/dl](https://go.dev/dl/)
- Walk through [How to Write Go Code](https://go.dev/doc/code) to learn how to fetch, build, and install Go code on your local machine

# Get Started
## Database
- Download Postgres (i.e the database that Mealbot uses) [here](https://www.postgresql.org/download/). See link for specific instructions for your OS.
- Create a Postgres database
- Setup the database schema by executing `schema.sql` (WARNING: it drops existing Mealbot tables first), e.g. `psql "$DATABASE_URL" -f schema.sql`
- NOTE: If the steps above for the database aren't super clear, please check out official Postgres documentation. For help on SQL syntax, check out [PostgreSQL Tutorial](http://www.postgresqltutorial.com/)

## Configuration
Everything is configured with environment variables:

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `DATABASE_URL` | yes | | Postgres connection string, e.g. `postgres://user:pass@localhost:5432/mealbot?sslmode=disable` |
| `PORT` | no | `8080` | Port the HTTP server listens on |
| `STATIC_DIR` | no | `./static` | Directory of static files served at `/` |
| `LOG_LEVEL` | no | `info` | `debug`, `info`, `warn` or `error` |
| `AUTH0_ISSUER` | no | `https://mealbot.auth0.com/` | Expected `iss` claim of access tokens |
| `AUTH0_AUDIENCE` | no | `https://mealbot-2.herokuapp.com/` | Expected `aud` claim of access tokens |
| `AUTH0_JWKS_URL` | no | `https://mealbot.auth0.com/.well-known/jwks.json` | Where the token signing keys are published |
| `MAILGUN_SMTP_LOGIN` | for `pair` | | Sender address of pairing emails |
| `MAILGUN_DOMAIN` | for `pair` | | Mailgun sending domain |
| `MAILGUN_API_KEY` | for `pair` | | Mailgun API key |

## Go
- Build the project (`go build ./cmd/mealbot`)
- Run the HTTP server (`DATABASE_URL=... ./mealbot`)
- Run every scheduled round that is due, and email the groups (`./mealbot pair`)
- Rebuild the members' "last round with" history from the stored pairs (`./mealbot migrate`)

## HTTP API
All routes except `/healthz` and the static files require an Auth0 access token
(`Authorization: Bearer <token>`); requests without a valid token get `401`.

| Route | Purpose |
| --- | --- |
| `GET /healthz` | Liveness check, answers `200 ok` (no authentication) |
| `GET /members?org=` / `POST /members?org=` | List active members / upload a CSV (multipart field `members`) |
| `GET /orgs?admin=` | Organizations managed by an admin |
| `POST /org?admin=` | Create an organization (`{"org": "name"}`) |
| `POST /crossmatchtrait?org=` | Set the cross match trait (`{"trait": "college"}`) |
| `GET /rounds?org=` | Scheduled round dates |
| `POST /round?org=&round=` | Schedule a round; add `roundId=` to reschedule it |
| `DELETE /round?org=&roundId=` | Cancel a round |
| `GET /pairs?org=` | Pairings of every round |

Malformed input is rejected with `400` (the original service returned `500`): a non-numeric `roundId`, a missing or malformed `members` upload, and an invalid or empty CSV.

## Tests
- `go test ./...` runs everything. The integration tests start Postgres with
  [testcontainers-go](https://golang.testcontainers.org/) and load `schema.sql`;
  they are skipped when Docker is not available (set `MEALBOT_REQUIRE_DOCKER=1` to fail instead).
- CI checks that `go.mod`/`go.sum` are tidy, then runs `go mod download`, `go build`, `go vet ./...`, a license check and `go test -race ./...` (see `.github/workflows/ci.yml`).
- `scripts/check-licenses.sh` runs the same license check locally (go-licenses; allowed: MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0, ISC, MPL-2.0).

## Layout
- `cmd/mealbot`: entry point (serve, `pair`, `migrate`)
- `internal/config`: environment configuration
- `internal/auth`: Auth0 JWT validation (golang-jwt v5, JWKS)
- `internal/httpapi`: routes, middleware (auth, then CORS) and handlers
- `internal/store`: SQL (pgx)
- `internal/pairing`: pairing algorithm and round runner
- `internal/mail`: Mailgun (v4) emails
- `internal/migration`: one-off data migrations
- `internal/server`, `internal/logging`, `internal/testutil`: shared helpers
- `schema.sql`: database schema; `static/`: static files; `testdata/`: sample CSVs

# Miscellanea
- Package management is handled w/ Go Modules (https://go.dev/blog/using-go-modules). `go.sum` is committed; keep it current with `go mod tidy`.
- Deployed on Heroku: see `Procfile`
