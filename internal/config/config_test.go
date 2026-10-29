package config

import "testing"

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg := Load(envFrom(nil))

	if cfg.Port != "8080" || cfg.StaticDir != "./static" || cfg.LogLevel != "info" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.Auth0.Issuer != "https://mealbot.auth0.com/" ||
		cfg.Auth0.Audience != "https://mealbot-2.herokuapp.com/" ||
		cfg.Auth0.JWKSURL != "https://mealbot.auth0.com/.well-known/jwks.json" {
		t.Errorf("unexpected auth0 defaults: %+v", cfg.Auth0)
	}
	if cfg.ValidateDatabase() == nil {
		t.Error("expected error without DATABASE_URL")
	}
	if cfg.ValidateMailgun() == nil {
		t.Error("expected error without Mailgun settings")
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg := Load(envFrom(map[string]string{
		"PORT":               "9000",
		"DATABASE_URL":       "postgres://x",
		"AUTH0_ISSUER":       "https://issuer/",
		"AUTH0_AUDIENCE":     "aud",
		"AUTH0_JWKS_URL":     "https://issuer/jwks",
		"MAILGUN_SMTP_LOGIN": "bot@example.com",
		"MAILGUN_DOMAIN":     "example.com",
		"MAILGUN_API_KEY":    "key",
	}))

	if cfg.Port != "9000" || cfg.DatabaseURL != "postgres://x" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.Auth0.Issuer != "https://issuer/" || cfg.Auth0.Audience != "aud" || cfg.Auth0.JWKSURL != "https://issuer/jwks" {
		t.Errorf("unexpected auth0 config: %+v", cfg.Auth0)
	}
	if err := cfg.ValidateDatabase(); err != nil {
		t.Error(err)
	}
	if err := cfg.ValidateMailgun(); err != nil {
		t.Error(err)
	}
}
