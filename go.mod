module github.com/johnamadeo/mealbot

// +heroku goVersion go1.24
// +heroku install ./cmd/...

go 1.25

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/jackc/pgx/v5 v5.7.1
	github.com/mailgun/mailgun-go v2.0.0+incompatible
	github.com/sirupsen/logrus v1.9.3
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	golang.org/x/mod v0.21.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

require (
	github.com/facebookgo/ensure v0.0.0-20200202191622-63f1cf65ac4c // indirect
	github.com/facebookgo/stack v0.0.0-20160209184415-751773369052 // indirect
	github.com/facebookgo/subset v0.0.0-20200203212716-c811ad88dec4 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/stretchr/testify v1.9.0
	golang.org/x/sync v0.8.0 // indirect
)

// NOTE: the indirect requirements below were written by hand. Run `go mod tidy`
// to regenerate go.sum and normalise this block.
require (
	github.com/gobuffalo/envy v1.6.11 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/joho/godotenv v1.3.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	golang.org/x/crypto v0.27.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)
