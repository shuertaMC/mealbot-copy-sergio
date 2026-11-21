package config

import "testing"

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestDefaults(t *testing.T) {
	cfg := FromLookup(lookupFrom(nil))
	if cfg.Port != "8080" || cfg.DatabaseURL != "" || cfg.LogLevel != "info" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.Auth0.Issuer != "https://mealbot.auth0.com/" ||
		cfg.Auth0.Audience != "https://mealbot-2.herokuapp.com/" ||
		cfg.Auth0.JWKSURL != "https://mealbot.auth0.com/.well-known/jwks.json" {
		t.Fatalf("unexpected auth0 defaults: %+v", cfg.Auth0)
	}
	if cfg.DBName != "mealbot" || cfg.DBUser == "" {
		t.Fatalf("unexpected db defaults: %+v", cfg)
	}
}

func TestOverrides(t *testing.T) {
	cfg := FromLookup(lookupFrom(map[string]string{
		"PORT":               "9000",
		"DATABASE_URL":       "postgres://u@h/db",
		"AUTH0_ISSUER":       "iss",
		"AUTH0_AUDIENCE":     "aud",
		"AUTH0_JWKS_URL":     "http://jwks",
		"MAILGUN_DOMAIN":     "d",
		"MAILGUN_API_KEY":    "k",
		"MAILGUN_SMTP_LOGIN": "l",
		"LOG_LEVEL":          "debug",
		"DB_USER":            "me",
		"DB_NAME":            "n",
	}))
	if cfg.Port != "9000" || cfg.DatabaseURL != "postgres://u@h/db" || cfg.LogLevel != "debug" ||
		cfg.Auth0.Issuer != "iss" || cfg.Auth0.Audience != "aud" || cfg.Auth0.JWKSURL != "http://jwks" ||
		cfg.Mailgun.Domain != "d" || cfg.Mailgun.APIKey != "k" || cfg.Mailgun.SMTPLogin != "l" ||
		cfg.DBUser != "me" || cfg.DBName != "n" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
}

func TestEmptyValuesFallBackToDefaults(t *testing.T) {
	cfg := FromLookup(lookupFrom(map[string]string{"PORT": ""}))
	if cfg.Port != "8080" {
		t.Fatalf("port = %q", cfg.Port)
	}
}
