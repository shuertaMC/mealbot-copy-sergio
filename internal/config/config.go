// Package config loads typed application configuration from environment
// variables.
package config

import "os"

// Defaults matching the values that used to be hard-coded.
const (
	DefaultPort         = "8080"
	DefaultAuth0Issuer  = "https://mealbot.auth0.com/"
	DefaultAuth0Aud     = "https://mealbot-2.herokuapp.com/"
	DefaultAuth0JWKSURL = "https://mealbot.auth0.com/.well-known/jwks.json"
	DefaultDBUser       = "johnamadeodaniswara"
	DefaultDBName       = "mealbot"
	DefaultLogLevel     = "info"
	DefaultStaticDir    = "./static"
	DefaultCSVDir       = "./csv/"
)

// Auth0 holds the JWT validation settings.
type Auth0 struct {
	// Issuer is the expected 'iss' claim (env AUTH0_ISSUER).
	Issuer string
	// Audience is the expected 'aud' claim (env AUTH0_AUDIENCE).
	Audience string
	// JWKSURL is where the signing keys are fetched from (env AUTH0_JWKS_URL).
	JWKSURL string
}

// Mailgun holds the Mailgun settings. Values may be empty; sending email
// fails with a descriptive error when they are missing.
type Mailgun struct {
	// SMTPLogin is used as the sender address (env MAILGUN_SMTP_LOGIN).
	SMTPLogin string
	// Domain is the Mailgun domain (env MAILGUN_DOMAIN).
	Domain string
	// APIKey is the Mailgun API key (env MAILGUN_API_KEY).
	APIKey string
}

// Config is the typed application configuration.
type Config struct {
	// Port the HTTP server listens on (env PORT).
	Port string
	// DatabaseURL is the Postgres connection URL (env DATABASE_URL). When
	// empty the local DBUser/DBName settings are used.
	DatabaseURL string
	// DBUser and DBName describe the local database (env DB_USER, DB_NAME).
	DBUser string
	DBName string
	Auth0  Auth0
	// Mailgun settings.
	Mailgun Mailgun
	// LogLevel is one of debug, info, warn, error (env LOG_LEVEL).
	LogLevel string
	// StaticDir is the directory served at "/" (env STATIC_DIR).
	StaticDir string
	// CSVDir is where uploaded member CSV files are stored (env CSV_DIR).
	CSVDir string
}

// Load reads the configuration from the process environment.
func Load() Config {
	return FromLookup(os.LookupEnv)
}

// FromLookup builds a Config using the given lookup function (os.LookupEnv
// semantics). Empty values are treated as unset.
func FromLookup(lookup func(string) (string, bool)) Config {
	get := func(key, def string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return def
	}

	return Config{
		Port:        get("PORT", DefaultPort),
		DatabaseURL: get("DATABASE_URL", ""),
		DBUser:      get("DB_USER", DefaultDBUser),
		DBName:      get("DB_NAME", DefaultDBName),
		Auth0: Auth0{
			Issuer:   get("AUTH0_ISSUER", DefaultAuth0Issuer),
			Audience: get("AUTH0_AUDIENCE", DefaultAuth0Aud),
			JWKSURL:  get("AUTH0_JWKS_URL", DefaultAuth0JWKSURL),
		},
		Mailgun: Mailgun{
			SMTPLogin: get("MAILGUN_SMTP_LOGIN", ""),
			Domain:    get("MAILGUN_DOMAIN", ""),
			APIKey:    get("MAILGUN_API_KEY", ""),
		},
		LogLevel:  get("LOG_LEVEL", DefaultLogLevel),
		StaticDir: get("STATIC_DIR", DefaultStaticDir),
		CSVDir:    get("CSV_DIR", DefaultCSVDir),
	}
}
