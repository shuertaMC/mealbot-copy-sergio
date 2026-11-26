# Testing

Run every command from the repository root. The required Go version is the `go` directive in [`go.mod`](go.mod) (Go 1.26).

## Default suite (self-contained)

The default suite needs no PostgreSQL server, network access, Auth0 or Mailgun credentials, and never contacts live providers. Authentication tests use locally generated RSA keys and fake JWKS responses; mail tests use an injected fake.

```sh
go test ./...                  # all packages
go test -race ./...            # with the race detector (needs a C toolchain)
go test -count=1 ./...         # bypass the test cache
```

## Coverage

Statement coverage of the first-party `internal/...` packages must stay at or above 80%:

```sh
go test -covermode=atomic -coverprofile=coverage.out ./internal/...
go tool cover -func=coverage.out | tail -n 1   # aggregate "total:" line
```

`coverage.out` is a generated file; do not commit it.

## PostgreSQL integration suite (opt-in)

These tests are compiled only with the `integration` build tag and additionally skip unless `RUN_POSTGRES_INTEGRATION=1` is set.

Prerequisites:

- PostgreSQL 17.
- An empty, disposable database whose name contains `test` as a delimited segment (for example `mealbot_test`). The suite resets that database and refuses any other name.
- `DATABASE_URL` pointing at it as an absolute `postgres://` or `postgresql://` URL.

```sh
export RUN_POSTGRES_INTEGRATION=1
export DATABASE_URL='postgresql://mealbot_test@127.0.0.1:5432/mealbot_test?sslmode=disable'
go test -count=1 -tags=integration ./...
```

Never point `DATABASE_URL` at a database holding data you want to keep.

See [README.md](README.md) for configuration and the remaining quality gates (`go vet`, `govulncheck`).
