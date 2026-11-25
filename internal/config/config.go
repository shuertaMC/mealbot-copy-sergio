// Package config loads all runtime settings (HTTP port, database, Mailgun and
// Auth0) from environment variables.
package config

import "os"

const (
	// DefaultPort is used when PORT is not set.
	DefaultPort = "8080"
	// DefaultDBName is the database used when neither DATABASE_URL nor DB_NAME is set.
	DefaultDBName = "mealbot"
	// DefaultDBSSLMode is the sslmode used for locally assembled connection strings.
	DefaultDBSSLMode = "disable"
	// DefaultAuth0Issuer is the 'iss' claim expected in access tokens.
	DefaultAuth0Issuer = "https://mealbot.auth0.com/"
	// DefaultAuth0Audience is the 'aud' claim expected in access tokens.
	DefaultAuth0Audience = "https://mealbot-2.herokuapp.com/"
	// JWKSPath is appended to the issuer when AUTH0_JWKS_URL is not set.
	JWKSPath = ".well-known/jwks.json"
)

// DB holds database connection settings. If URL is non-empty it is used as is
// (e.g. Heroku's DATABASE_URL); otherwise a key/value DSN is assembled from the
// remaining fields. An empty User means "let the driver pick the OS user".
type DB struct {
	URL      string
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
}

// Mailgun holds the credentials used for sending pairing emails.
type Mailgun struct {
	SMTPLogin string
	Domain    string
	APIKey    string
}

// Auth0 holds the JWT validation settings.
type Auth0 struct {
	Issuer   string
	Audience string
	JWKSURL  string
}

// Config is the complete application configuration.
type Config struct {
	Port    string
	DB      DB
	Mailgun Mailgun
	Auth0   Auth0
}

// Load reads the configuration from the process environment.
func Load() Config {
	return FromLookup(os.LookupEnv)
}

// FromLookup builds a Config using the supplied lookup function (os.LookupEnv
// in production, a map-backed function in tests).
func FromLookup(lookup func(string) (string, bool)) Config {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return fallback
	}

	cfg := Config{
		Port: get("PORT", DefaultPort),
		DB: DB{
			URL:      get("DATABASE_URL", ""),
			Host:     get("DB_HOST", ""),
			Port:     get("DB_PORT", ""),
			User:     get("DB_USER", ""),
			Password: get("DB_PASSWORD", ""),
			Name:     get("DB_NAME", DefaultDBName),
			SSLMode:  get("DB_SSLMODE", DefaultDBSSLMode),
		},
		Mailgun: Mailgun{
			SMTPLogin: get("MAILGUN_SMTP_LOGIN", ""),
			Domain:    get("MAILGUN_DOMAIN", ""),
			APIKey:    get("MAILGUN_API_KEY", ""),
		},
		Auth0: Auth0{
			Issuer:   get("AUTH0_ISSUER", DefaultAuth0Issuer),
			Audience: get("AUTH0_AUDIENCE", DefaultAuth0Audience),
		},
	}
	cfg.Auth0.JWKSURL = get("AUTH0_JWKS_URL", cfg.Auth0.Issuer+JWKSPath)
	return cfg
}
