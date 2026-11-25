// Package config owns the process configuration value used by the composition
// root. Loading is explicit so tests and future command-specific validation do
// not need to mutate global process state.
package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/johnamadeo/mealbot/internal/httpapi"
)

const (
	DefaultPort               = "8080"
	DefaultAuthIssuer         = "https://mealbot.auth0.com/"
	DefaultAudience           = "https://mealbot-2.herokuapp.com/"
	DefaultJWKSURL            = "https://mealbot.auth0.com/.well-known/jwks.json"
	DefaultCORSAllowedHeaders = httpapi.AllowedHeaders
	DefaultCORSAllowedMethods = httpapi.AllowedMethods

	CommandWeb     = "web"
	CommandPair    = "pair"
	CommandMigrate = "migrate"

	TestModeEnvironment = "MEALBOT_TEST_MODE"
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
	CORSAllowedHeaders string
	CORSAllowedMethods string
	MailgunDomain      string
	MailgunAPIKey      string
	MailgunSMTPLogin   string
	TestMode           bool
}

// Load captures supported environment variables and applies rollout-compatible
// defaults. getenv is injected to keep loading deterministic in tests.
func Load(getenv func(string) string) Config {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}

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
		Port:               port,
		DatabaseURL:        getenv("DATABASE_URL"),
		AuthIssuer:         issuer,
		AuthAudience:       audience,
		AuthJWKSURL:        jwksURL,
		ReflectCORSOrigin:  true,
		CORSAllowedHeaders: DefaultCORSAllowedHeaders,
		CORSAllowedMethods: DefaultCORSAllowedMethods,
		MailgunDomain:      getenv("MAILGUN_DOMAIN"),
		MailgunAPIKey:      getenv("MAILGUN_API_KEY"),
		MailgunSMTPLogin:   getenv("MAILGUN_SMTP_LOGIN"),
		TestMode:           strings.EqualFold(strings.TrimSpace(getenv(TestModeEnvironment)), "true"),
	}
}

// Validate checks only the settings consumed by command. The pair command
// shares the web command's database and Auth0 requirements and additionally
// requires Mailgun credentials unless the snapshot explicitly enables test
// mode. Validation errors name settings, never their values.
func (c Config) Validate(command string) error {
	required := []setting{}
	switch command {
	case CommandWeb:
		required = c.databaseAndAuthSettings()
	case CommandPair:
		required = c.databaseAndAuthSettings()
		if !c.TestMode {
			required = append(required,
				setting{name: "MAILGUN_DOMAIN", value: c.MailgunDomain},
				setting{name: "MAILGUN_API_KEY", value: c.MailgunAPIKey},
				setting{name: "MAILGUN_SMTP_LOGIN", value: c.MailgunSMTPLogin},
			)
		}
	case CommandMigrate:
		required = append(required, setting{name: "DATABASE_URL", value: c.DatabaseURL})
	default:
		return fmt.Errorf("unknown command %q", command)
	}

	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("%s requires setting %s", command, item.name)
		}
	}

	if command != CommandMigrate {
		if err := validateWebURL("AUTH0_ISSUER", c.AuthIssuer, true); err != nil {
			return err
		}
		if err := validateWebURL("AUTH0_AUDIENCE", c.AuthAudience, false); err != nil {
			return err
		}
		if err := validateWebURL("AUTH0_JWKS_URL", c.AuthJWKSURL, true); err != nil {
			return err
		}
	}

	return nil
}

// ValidateForCommand is the descriptive form of Validate for composition-root
// callers.
func (c Config) ValidateForCommand(command string) error {
	return c.Validate(command)
}

type setting struct {
	name  string
	value string
}

func (c Config) databaseAndAuthSettings() []setting {
	return []setting{
		{name: "DATABASE_URL", value: c.DatabaseURL},
		{name: "AUTH0_ISSUER", value: c.AuthIssuer},
		{name: "AUTH0_AUDIENCE", value: c.AuthAudience},
		{name: "AUTH0_JWKS_URL", value: c.AuthJWKSURL},
	}
}

func validateWebURL(name, value string, requireHost bool) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !parsed.IsAbs() || (requireHost && parsed.Host == "") {
		return fmt.Errorf("setting %s must be an absolute URL", name)
	}
	return nil
}

// Address returns the net/http listen address matching the legacy PORT
// behavior.
func (c Config) Address() string {
	return ":" + c.Port
}
