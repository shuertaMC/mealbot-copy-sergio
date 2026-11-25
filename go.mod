module github.com/johnamadeo/mealbot

// +heroku goVersion go1.24
// +heroku install ./cmd/...

go 1.24

require (
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/jackc/pgx/v5 v5.7.1
	github.com/mailgun/mailgun-go v2.0.0+incompatible
	github.com/sirupsen/logrus v1.9.3
	github.com/stretchr/testify v1.9.0
)

// NOTE: the indirect requirements below were written by hand. Run `go mod tidy`
// to regenerate go.sum and normalise this block.
require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/gobuffalo/envy v1.6.11 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/joho/godotenv v1.3.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rogpeppe/go-internal v1.0.0 // indirect
	golang.org/x/crypto v0.27.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
