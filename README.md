# Intro (for people new to Go)
- Check out [A Tour of Go](https://tour.golang.org/welcome/1) to learn Go syntax
- Download Go [Downloads](https://golang.org/dl/) (Go 1.24 or newer is required)
- Walk through [How to Write Go Code](https://golang.org/doc/code.html) to learn how to fetch, build, and install Go code on your local machine

# Get Started
## Database
- Download Postgres (i.e the database that Mealbot uses) [here](https://www.postgresql.org/download/). See link for specific instructions for your OS.
- Create a Postgres database (the default name is `mealbot`)
- Setup the database schema by executing 'schema.sql'
- Tell Mealbot how to connect with environment variables (see below). Nothing needs to be edited in the source code.
- NOTE: If the steps above for the database aren't super clear, please check out official Postgres documentation. For help on SQL syntax, check out [PostgreSQL Tutorial](http://www.postgresqltutorial.com/)

## Configuration
All settings are read from environment variables by `internal/config`.

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP port of the server |
| `DATABASE_URL` | (unset) | Full Postgres connection string (set automatically on Heroku). If set, the `DB_*` variables are ignored |
| `DB_HOST` | (unset) | Postgres host (driver default when unset) |
| `DB_PORT` | (unset) | Postgres port |
| `DB_USER` | (unset) | Postgres user (driver default, normally the OS user, when unset) |
| `DB_PASSWORD` | (unset) | Postgres password |
| `DB_NAME` | `mealbot` | Database name |
| `DB_SSLMODE` | `disable` | Postgres sslmode |
| `MAILGUN_SMTP_LOGIN` | (unset) | Sender address login; required when `pair` sends emails |
| `MAILGUN_DOMAIN` | (unset) | Mailgun domain; required when `pair` sends emails |
| `MAILGUN_API_KEY` | (unset) | Mailgun API key; required when `pair` sends emails |
| `AUTH0_ISSUER` | `https://mealbot.auth0.com/` | Expected `iss` claim of access tokens |
| `AUTH0_AUDIENCE` | `https://mealbot-2.herokuapp.com/` | Expected `aud` claim of access tokens |
| `AUTH0_JWKS_URL` | `<AUTH0_ISSUER>.well-known/jwks.json` | Location of the signing keys |

Example for local development:

```
export DB_USER=myuser DB_NAME=mealbot
```

## Go
- Fetch dependencies and generate `go.sum` ('go mod tidy')
- Run the server ('go run ./cmd/mealbot'); it listens on `$PORT` (default 8080). For example `curl -i localhost:8080/privacy.html` serves a static page, and the API routes (`/members`, `/orgs`, `/org`, `/crossmatchtrait`, `/rounds`, `/round`, `/pairs`) require an Auth0 bearer token
- Build an executable ('go build -o bin/mealbot ./cmd/mealbot') and run it ('./bin/mealbot')
- Run the pairing scheduler once ('./bin/mealbot pair' or 'go run ./cmd/mealbot pair'), normally from a cron/Heroku Scheduler job
- Backfill the "last round with" data ('./bin/mealbot migrate')
- Run vet and tests ('go vet ./...' and 'go test ./...')

The server must be started from the repository root, since it serves `./static` and stores uploaded CSV files in `./csv`.

## Layout
- `cmd/mealbot`: entrypoint (server, `pair` and `migrate`)
- `internal/config`: environment configuration
- `internal/db`: connection helpers, `JSONB`, duplicate-key detection and the mockable `Querier` interface
- `internal/httputil`: middleware chain, CORS, query parameter and logging helpers
- `internal/auth`: Auth0 JWT middleware
- `internal/pairing`: pure pairing algorithm (`RunPairingAlgorithm`) and its types
- `internal/rounds`: round handlers, pairing round runner and Mailgun mailer
- `internal/members`, `internal/org`, `internal/pairs`: handlers and queries for members, organizations and pairs

# Miscellanea
- Package management is handled w/ Go Modules (https://blog.golang.org/using-go-modules)
- Deployment on Heroku uses `Procfile` (`web: bin/mealbot`), which the Go buildpack produces from `./cmd/...`
