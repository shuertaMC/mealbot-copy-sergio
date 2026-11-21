module github.com/johnamadeo/mealbot

go 1.25

// Heroku Go buildpack settings: build only the server binary (bin/mealbot).
// +heroku goVersion go1.25
// +heroku install ./cmd/mealbot

// NOTE: only direct dependencies are listed. Run `go mod tidy` to add the
// indirect requirements and regenerate go.sum (go.sum is intentionally empty).
require (
	github.com/MicahParks/keyfunc/v3 v3.3.5
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/jackc/pgx/v5 v5.7.2
	github.com/mailgun/mailgun-go/v4 v4.12.0
	github.com/testcontainers/testcontainers-go v0.37.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.37.0
)
