// Package config loads runtime configuration from environment variables.
package config

import (
	"errors"
	"os"
)

// Defaults for the Auth0 tenant Mealbot has always used. They keep existing
// deployments working when the AUTH0_* variables are not set.
const (
	DefaultPort          = "8080"
	DefaultStaticDir     = "./static"
	DefaultLogLevel      = "info"
	DefaultAuth0Issuer   = "https://mealbot.auth0.com/"
	DefaultAuth0Audience = "https://mealbot-2.herokuapp.com/"
	DefaultAuth0JWKSURL  = "https://mealbot.auth0.com/.well-known/jwks.json"
)

// Auth0 holds the settings used to validate access tokens.
type Auth0 struct {
	// Issuer is the expected value of the 'iss' claim (AUTH0_ISSUER).
	Issuer string
	// Audience is the expected value of the 'aud' claim (AUTH0_AUDIENCE).
	Audience string
	// JWKSURL is where the token signing keys are published (AUTH0_JWKS_URL).
	JWKSURL string
}

// Mailgun holds the settings used to send pairing emails.
type Mailgun struct {
	// SMTPLogin is the sender address (MAILGUN_SMTP_LOGIN).
	SMTPLogin string
	// Domain is the Mailgun sending domain (MAILGUN_DOMAIN).
	Domain string
	// APIKey is the Mailgun private API key (MAILGUN_API_KEY).
	APIKey string
}

// Config is the complete runtime configuration.
type Config struct {
	// Port to listen on (PORT).
	Port string
	// DatabaseURL is the Postgres connection string (DATABASE_URL).
	DatabaseURL string
	// StaticDir is the directory served at / (STATIC_DIR).
	StaticDir string
	// LogLevel is one of debug, info, warn, error (LOG_LEVEL).
	LogLevel string
	// Auth0 token validation settings.
	Auth0 Auth0
	// Mailgun email settings.
	Mailgun Mailgun
}

// Load builds a Config using getenv to look up variables.
func Load(getenv func(string) string) Config {
	get := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}

	return Config{
		Port:        get("PORT", DefaultPort),
		DatabaseURL: getenv("DATABASE_URL"),
		StaticDir:   get("STATIC_DIR", DefaultStaticDir),
		LogLevel:    get("LOG_LEVEL", DefaultLogLevel),
		Auth0: Auth0{
			Issuer:   get("AUTH0_ISSUER", DefaultAuth0Issuer),
			Audience: get("AUTH0_AUDIENCE", DefaultAuth0Audience),
			JWKSURL:  get("AUTH0_JWKS_URL", DefaultAuth0JWKSURL),
		},
		Mailgun: Mailgun{
			SMTPLogin: getenv("MAILGUN_SMTP_LOGIN"),
			Domain:    getenv("MAILGUN_DOMAIN"),
			APIKey:    getenv("MAILGUN_API_KEY"),
		},
	}
}

// FromEnv builds a Config from the process environment.
func FromEnv() Config {
	return Load(os.Getenv)
}

// ValidateDatabase reports whether a database URL is configured.
func (c Config) ValidateDatabase() error {
	if c.DatabaseURL == "" {
		return errors.New("environment variable DATABASE_URL not set")
	}
	return nil
}

// ValidateMailgun reports which Mailgun setting is missing, if any.
func (c Config) ValidateMailgun() error {
	switch {
	case c.Mailgun.SMTPLogin == "":
		return errors.New("environment variable MAILGUN_SMTP_LOGIN not set")
	case c.Mailgun.Domain == "":
		return errors.New("environment variable MAILGUN_DOMAIN not set")
	case c.Mailgun.APIKey == "":
		return errors.New("environment variable MAILGUN_API_KEY not set")
	}
	return nil
}
