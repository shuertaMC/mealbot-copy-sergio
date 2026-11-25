// Package config owns the process configuration value used by the composition
// root. Loading is explicit so tests and future command-specific validation do
// not need to mutate global process state.
package config

const (
	DefaultPort       = "8080"
	DefaultAuthIssuer = "https://mealbot.auth0.com/"
	DefaultAudience   = "https://mealbot-2.herokuapp.com/"
	DefaultJWKSURL    = "https://mealbot.auth0.com/.well-known/jwks.json"
)

// Config is the immutable-by-convention startup snapshot passed to service
// components. Secrets are values only; this package never logs them.
type Config struct {
	Port               string
	DatabaseURL        string
	AuthIssuer         string
	AuthAudience       string
	AuthJWKSURL        string
	ReflectCORSOrigin  bool
	MailgunDomain      string
	MailgunAPIKey      string
	MailgunSMTPLogin   string
}

// Load captures supported environment variables and applies rollout-compatible
// defaults. getenv is injected to keep loading deterministic in tests.
func Load(getenv func(string) string) Config {
	port := getenv("PORT")
	if port == "" {
		port = DefaultPort
	}

	issuer := getenv("AUTH0_ISSUER")
	if issuer == "" {
		issuer = DefaultAuthIssuer
	}
	audience := getenv("AUTH0_AUDIENCE")
	if audience == "" {
		audience = DefaultAudience
	}
	jwksURL := getenv("AUTH0_JWKS_URL")
	if jwksURL == "" {
		jwksURL = DefaultJWKSURL
	}

	return Config{
		Port:              port,
		DatabaseURL:       getenv("DATABASE_URL"),
		AuthIssuer:        issuer,
		AuthAudience:      audience,
		AuthJWKSURL:       jwksURL,
		ReflectCORSOrigin: true,
		MailgunDomain:     getenv("MAILGUN_DOMAIN"),
		MailgunAPIKey:     getenv("MAILGUN_API_KEY"),
		MailgunSMTPLogin:  getenv("MAILGUN_SMTP_LOGIN"),
	}
}

// Address returns the net/http listen address matching the legacy PORT
// behavior.
func (c Config) Address() string {
	return ":" + c.Port
}
