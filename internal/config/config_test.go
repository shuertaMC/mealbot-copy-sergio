package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadAppliesCompatibilityDefaults(t *testing.T) {
	got := Load(func(string) string { return "" })
	want := Config{
		Port:               DefaultPort,
		AuthIssuer:         DefaultAuthIssuer,
		AuthAudience:       DefaultAudience,
		AuthJWKSURL:        DefaultJWKSURL,
		ReflectCORSOrigin:  true,
		CORSAllowedHeaders: DefaultCORSAllowedHeaders,
		CORSAllowedMethods: DefaultCORSAllowedMethods,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load defaults = %#v, want %#v", got, want)
	}
	if got.Address() != ":8080" {
		t.Fatalf("Address() = %q, want %q", got.Address(), ":8080")
	}
}

func TestLoadCapturesOverridesByValue(t *testing.T) {
	environment := map[string]string{
		"PORT":               "9090",
		"DATABASE_URL":       "postgres://db.example/mealbot",
		"AUTH0_ISSUER":       "https://issuer.example/",
		"AUTH0_AUDIENCE":     "urn:mealbot:test",
		"AUTH0_JWKS_URL":     "https://issuer.example/keys",
		"MAILGUN_DOMAIN":     "mail.example",
		"MAILGUN_API_KEY":    "mail-key",
		"MAILGUN_SMTP_LOGIN": "sender@mail.example",
		TestModeEnvironment:   " TRUE ",
	}
	getenv := func(name string) string { return environment[name] }

	got := Load(getenv)
	want := Config{
		Port:                "9090",
		DatabaseURL:         "postgres://db.example/mealbot",
		AuthIssuer:          "https://issuer.example/",
		AuthAudience:        "urn:mealbot:test",
		AuthJWKSURL:         "https://issuer.example/keys",
		ReflectCORSOrigin:   true,
		CORSAllowedHeaders:  DefaultCORSAllowedHeaders,
		CORSAllowedMethods:  DefaultCORSAllowedMethods,
		MailgunDomain:       "mail.example",
		MailgunAPIKey:       "mail-key",
		MailgunSMTPLogin:    "sender@mail.example",
		TestMode:            true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load overrides = %#v, want %#v", got, want)
	}
	if got.Address() != ":9090" {
		t.Fatalf("Address() = %q, want %q", got.Address(), ":9090")
	}

	environment["PORT"] = "7070"
	environment["DATABASE_URL"] = "changed"
	if got.Port != "9090" || got.DatabaseURL != "postgres://db.example/mealbot" {
		t.Fatalf("loaded snapshot changed after source mutation: %#v", got)
	}
	if again := Load(getenv); again.Port != "7070" || again.DatabaseURL != "changed" {
		t.Fatalf("subsequent snapshot did not capture new values: %#v", again)
	}
}

func TestLoadRequiresExplicitTrueForTestMode(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{value: "true", want: true},
		{value: " TRUE ", want: true},
		{value: "false", want: false},
		{value: "1", want: false},
		{value: "yes", want: false},
		{value: "", want: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			got := Load(func(name string) string {
				if name == TestModeEnvironment {
					return test.value
				}
				return ""
			})
			if got.TestMode != test.want {
				t.Fatalf("TestMode = %v, want %v for %q", got.TestMode, test.want, test.value)
			}
		})
	}
}

func TestValidateReportsEveryRequiredSetting(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		clear   func(*Config)
	}{
		{name: "database", setting: "DATABASE_URL", clear: func(c *Config) { c.DatabaseURL = "" }},
		{name: "issuer", setting: "AUTH0_ISSUER", clear: func(c *Config) { c.AuthIssuer = "" }},
		{name: "audience", setting: "AUTH0_AUDIENCE", clear: func(c *Config) { c.AuthAudience = "" }},
		{name: "jwks", setting: "AUTH0_JWKS_URL", clear: func(c *Config) { c.AuthJWKSURL = "" }},
		{name: "mailgun domain", setting: "MAILGUN_DOMAIN", clear: func(c *Config) { c.MailgunDomain = "" }},
		{name: "mailgun api key", setting: "MAILGUN_API_KEY", clear: func(c *Config) { c.MailgunAPIKey = " \t" }},
		{name: "mailgun sender", setting: "MAILGUN_SMTP_LOGIN", clear: func(c *Config) { c.MailgunSMTPLogin = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := completeConfig()
			test.clear(&cfg)
			err := cfg.Validate(CommandPair)
			if err == nil || !strings.Contains(err.Error(), test.setting) {
				t.Fatalf("Validate(pair) error = %v, want setting %s", err, test.setting)
			}
		})
	}
}

func TestValidateDistinguishesCommands(t *testing.T) {
	t.Run("web does not require mailgun", func(t *testing.T) {
		cfg := completeConfig()
		cfg.MailgunDomain = ""
		cfg.MailgunAPIKey = ""
		cfg.MailgunSMTPLogin = ""
		if err := cfg.ValidateForCommand(CommandWeb); err != nil {
			t.Fatalf("Validate(web) error = %v", err)
		}
	})

	t.Run("pair requires mailgun", func(t *testing.T) {
		cfg := completeConfig()
		cfg.MailgunDomain = ""
		if err := cfg.Validate(CommandPair); err == nil || !strings.Contains(err.Error(), "MAILGUN_DOMAIN") {
			t.Fatalf("Validate(pair) error = %v", err)
		}
	})

	t.Run("pair test mode exempts only mailgun", func(t *testing.T) {
		cfg := completeConfig()
		cfg.TestMode = true
		cfg.MailgunDomain = ""
		cfg.MailgunAPIKey = ""
		cfg.MailgunSMTPLogin = ""
		if err := cfg.Validate(CommandPair); err != nil {
			t.Fatalf("Validate(pair test mode) error = %v", err)
		}
		cfg.DatabaseURL = ""
		if err := cfg.Validate(CommandPair); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Fatalf("Validate(pair test mode without database) error = %v", err)
		}
	})

	t.Run("migrate requires only database", func(t *testing.T) {
		cfg := Config{DatabaseURL: "postgres://db.example/mealbot"}
		if err := cfg.Validate(CommandMigrate); err != nil {
			t.Fatalf("Validate(migrate) error = %v", err)
		}
		cfg.DatabaseURL = ""
		if err := cfg.Validate(CommandMigrate); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Fatalf("Validate(migrate without database) error = %v", err)
		}
	})
}

func TestValidateRejectsUnusableAuthURLsWithoutValues(t *testing.T) {
	tests := []struct {
		name    string
		setting string
		secret  string
		set     func(*Config, string)
	}{
		{name: "issuer", setting: "AUTH0_ISSUER", secret: "issuer-secret", set: func(c *Config, value string) { c.AuthIssuer = value }},
		{name: "audience", setting: "AUTH0_AUDIENCE", secret: "audience-secret", set: func(c *Config, value string) { c.AuthAudience = value }},
		{name: "jwks", setting: "AUTH0_JWKS_URL", secret: "jwks-secret", set: func(c *Config, value string) { c.AuthJWKSURL = value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := completeConfig()
			test.set(&cfg, test.secret)
			err := cfg.Validate(CommandWeb)
			if err == nil || !strings.Contains(err.Error(), test.setting) {
				t.Fatalf("Validate(web) error = %v, want setting %s", err, test.setting)
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("validation error exposed setting value: %q", err)
			}
		})
	}
}

func TestValidationErrorsNeverExposeSecrets(t *testing.T) {
	cfg := completeConfig()
	cfg.DatabaseURL = ""
	secrets := []string{cfg.AuthIssuer, cfg.AuthAudience, cfg.AuthJWKSURL, cfg.MailgunDomain, cfg.MailgunAPIKey, cfg.MailgunSMTPLogin}
	err := cfg.Validate(CommandPair)
	if err == nil {
		t.Fatal("Validate(pair) succeeded without DATABASE_URL")
	}
	for _, secret := range secrets {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("validation error %q exposed secret value %q", err, secret)
		}
	}
}

func TestValidateRejectsUnknownCommand(t *testing.T) {
	err := completeConfig().Validate("serve-and-delete")
	if err == nil || err.Error() != `unknown command "serve-and-delete"` {
		t.Fatalf("unknown command error = %v", err)
	}
}

func completeConfig() Config {
	return Config{
		Port:               DefaultPort,
		DatabaseURL:        "postgres://user:database-secret@db.example/mealbot",
		AuthIssuer:         "https://issuer.example/",
		AuthAudience:       "https://api.example/",
		AuthJWKSURL:        "https://issuer.example/.well-known/jwks.json",
		ReflectCORSOrigin:  true,
		CORSAllowedHeaders: DefaultCORSAllowedHeaders,
		CORSAllowedMethods: DefaultCORSAllowedMethods,
		MailgunDomain:      "mail-secret.example",
		MailgunAPIKey:      "api-secret-value",
		MailgunSMTPLogin:   "sender-secret@mail.example",
	}
}
