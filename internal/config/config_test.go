package config

import (
	"strings"
	"testing"
	"time"
)

func TestFromMapAppliesDevelopmentDefaults(t *testing.T) {
	configured, err := FromMap(map[string]string{
		"DB_USER": "local-user",
		"DB_NAME": "local-name",
	})
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	if configured.Mode() != Development {
		t.Errorf("Mode() = %q, want %q", configured.Mode(), Development)
	}
	if configured.Port() != defaultPort {
		t.Errorf("Port() = %d, want %d", configured.Port(), defaultPort)
	}
	if got := configured.Database(); got != (Database{User: "local-user", Name: "local-name"}) {
		t.Errorf("Database() = %#v", got)
	}
	if got := configured.Auth0(); got != (Auth0{
		Issuer:   "https://mealbot.auth0.com/",
		Audience: "https://mealbot-2.herokuapp.com/",
		JWKSURL:  "https://mealbot.auth0.com/.well-known/jwks.json",
	}) {
		t.Errorf("Auth0() = %#v", got)
	}
	if got := configured.Mailgun(); got != (Mailgun{}) {
		t.Errorf("Mailgun() = %#v, want zero value", got)
	}
	if configured.StaticDir() != defaultStaticDir {
		t.Errorf("StaticDir() = %q, want %q", configured.StaticDir(), defaultStaticDir)
	}
	if origins := configured.AllowedCORSOrigins(); len(origins) != 0 {
		t.Errorf("AllowedCORSOrigins() = %#v, want empty", origins)
	}
	if configured.RequestTimeout() != defaultRequestTimeout {
		t.Errorf("RequestTimeout() = %v", configured.RequestTimeout())
	}
	if configured.JWKSTimeout() != defaultJWKSTimeout {
		t.Errorf("JWKSTimeout() = %v", configured.JWKSTimeout())
	}
	if configured.MailTimeout() != defaultMailTimeout {
		t.Errorf("MailTimeout() = %v", configured.MailTimeout())
	}
}

func TestFromMapAcceptsCompleteDevelopmentAuth0Override(t *testing.T) {
	configured, err := FromMap(map[string]string{
		"DB_USER":        "local-user",
		"DB_NAME":        "local-name",
		"AUTH0_ISSUER":   "https://tenant.example.test/",
		"AUTH0_AUDIENCE": "https://api.example.test/",
		"AUTH0_JWKS_URL": "https://tenant.example.test/.well-known/jwks.json",
	})
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	want := Auth0{
		Issuer:   "https://tenant.example.test/",
		Audience: "https://api.example.test/",
		JWKSURL:  "https://tenant.example.test/.well-known/jwks.json",
	}
	if got := configured.Auth0(); got != want {
		t.Errorf("Auth0() = %#v, want %#v", got, want)
	}
	if got := configured.Mailgun(); got != (Mailgun{}) {
		t.Errorf("Mailgun() = %#v, want zero value", got)
	}
}

func TestFromMapParsesCompleteProductionConfiguration(t *testing.T) {
	configured, err := FromMap(completeProductionMap())
	if err != nil {
		t.Fatalf("FromMap() error = %v", err)
	}

	if configured.Mode() != Production || configured.Port() != 9443 {
		t.Errorf("mode/port = %q/%d", configured.Mode(), configured.Port())
	}
	if got := configured.Database(); got.URL != "postgresql://db.example.test/mealbot" || got.User != "" || got.Name != "" {
		t.Errorf("Database() = %#v", got)
	}
	if got := configured.Auth0(); got != (Auth0{
		Issuer:   "https://tenant.example.test/",
		Audience: "https://api.example.test",
		JWKSURL:  "https://tenant.example.test/.well-known/jwks.json",
	}) {
		t.Errorf("Auth0() = %#v", got)
	}
	if got := configured.Mailgun(); got != (Mailgun{
		SMTPLogin: "mail@example.test",
		Domain:    "mg.example.test",
		APIKey:    "test-key",
	}) {
		t.Errorf("Mailgun() = %#v", got)
	}
	if configured.StaticDir() != "/srv/static" {
		t.Errorf("StaticDir() = %q", configured.StaticDir())
	}
	wantOrigins := []string{"https://app.example.test", "http://localhost:3000"}
	assertStrings(t, configured.AllowedCORSOrigins(), wantOrigins)
	if configured.RequestTimeout() != 20*time.Second || configured.JWKSTimeout() != 3*time.Second || configured.MailTimeout() != 7*time.Second {
		t.Errorf("timeouts = %v/%v/%v", configured.RequestTimeout(), configured.JWKSTimeout(), configured.MailTimeout())
	}
}

func TestNewTrimsValuesAndProtectsMutableInput(t *testing.T) {
	origins := []string{" https://app.example.test/ "}
	configured, err := New(Values{
		Mode:               Mode(" DEVELOPMENT "),
		DatabaseURL:        " postgresql://localhost/mealbot ",
		AllowedCORSOrigins: origins,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	origins[0] = "https://changed.example.test"
	firstRead := configured.AllowedCORSOrigins()
	firstRead[0] = "https://also-changed.example.test"

	if configured.Mode() != Development {
		t.Errorf("Mode() = %q", configured.Mode())
	}
	if got := configured.Database().URL; got != "postgresql://localhost/mealbot" {
		t.Errorf("Database().URL = %q", got)
	}
	assertStrings(t, configured.AllowedCORSOrigins(), []string{"https://app.example.test"})
}

func TestFromLookupReadsInjectedSource(t *testing.T) {
	requested := make(map[string]int)
	values := map[string]string{"DB_USER": "tester", "DB_NAME": "mealbot_test"}
	configured, err := FromLookup(func(key string) (string, bool) {
		requested[key]++
		value, ok := values[key]
		return value, ok
	})
	if err != nil {
		t.Fatalf("FromLookup() error = %v", err)
	}
	if configured.Database().User != "tester" {
		t.Errorf("Database().User = %q", configured.Database().User)
	}
	if requested["DB_USER"] != 1 {
		t.Errorf("DB_USER lookup count = %d, want 1", requested["DB_USER"])
	}
	if _, err := FromLookup(nil); err == nil {
		t.Fatal("FromLookup(nil) error = nil, want error")
	}
}

func TestFromMapRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		changes map[string]string
		remove  []string
		want    string
	}{
		{name: "port syntax", changes: map[string]string{"PORT": "eight"}, want: "PORT must be an integer"},
		{name: "port zero", changes: map[string]string{"PORT": "0"}, want: "PORT must be between"},
		{name: "port too large", changes: map[string]string{"PORT": "65536"}, want: "PORT must be between"},
		{name: "request duration syntax", changes: map[string]string{"REQUEST_TIMEOUT": "soon"}, want: "REQUEST_TIMEOUT must be a duration"},
		{name: "request duration zero", changes: map[string]string{"REQUEST_TIMEOUT": "0s"}, want: "REQUEST_TIMEOUT must be positive"},
		{name: "jwks duration negative", changes: map[string]string{"JWKS_TIMEOUT": "-1s"}, want: "JWKS_TIMEOUT must be positive"},
		{name: "mail duration zero", changes: map[string]string{"MAIL_TIMEOUT": "0s"}, want: "MAIL_TIMEOUT must be positive"},
		{name: "unknown mode", changes: map[string]string{"APP_ENV": "staging"}, want: "APP_ENV must be"},
		{name: "missing database", remove: []string{"DATABASE_URL"}, want: "DATABASE_URL or both DB_USER and DB_NAME"},
		{name: "partial local database", remove: []string{"DATABASE_URL"}, changes: map[string]string{"DB_USER": "local"}, want: "DATABASE_URL or both DB_USER and DB_NAME"},
		{name: "mixed database", changes: map[string]string{"DB_USER": "local", "DB_NAME": "mealbot"}, want: "not both"},
		{name: "relative database URL", changes: map[string]string{"DATABASE_URL": "localhost/mealbot"}, want: "absolute PostgreSQL URL"},
		{name: "database scheme", changes: map[string]string{"DATABASE_URL": "mysql://db.example.test/mealbot"}, want: "postgres or postgresql"},
		{name: "database URL without database path", changes: map[string]string{"DATABASE_URL": "postgresql://db.example.test"}, want: "must select a database"},
		{name: "database URL root path", changes: map[string]string{"DATABASE_URL": "postgresql://db.example.test/"}, want: "must select a database"},
		{name: "issuer URL", changes: map[string]string{"AUTH0_ISSUER": "tenant.example.test"}, want: "AUTH0_ISSUER must be"},
		{name: "issuer fragment", changes: map[string]string{"AUTH0_ISSUER": "https://tenant.example.test/#fragment"}, want: "must not contain"},
		{name: "jwks URL", changes: map[string]string{"AUTH0_JWKS_URL": "file:///keys.json"}, want: "AUTH0_JWKS_URL must be"},
		{name: "origin wildcard", changes: map[string]string{"CORS_ALLOWED_ORIGINS": "*"}, want: "invalid CORS origin"},
		{name: "origin path", changes: map[string]string{"CORS_ALLOWED_ORIGINS": "https://app.example.test/path"}, want: "only scheme and host"},
		{name: "origin query", changes: map[string]string{"CORS_ALLOWED_ORIGINS": "https://app.example.test?debug=true"}, want: "only scheme and host"},
		{name: "empty origin entry", changes: map[string]string{"CORS_ALLOWED_ORIGINS": "https://app.example.test,,http://localhost:3000"}, want: "empty origin"},
		{name: "duplicate origin", changes: map[string]string{"CORS_ALLOWED_ORIGINS": "https://app.example.test,https://app.example.test/"}, want: "duplicate CORS origin"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := completeProductionMap()
			for _, key := range test.remove {
				delete(values, key)
			}
			for key, value := range test.changes {
				values[key] = value
			}
			_, err := FromMap(values)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("FromMap() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNewRejectsTypedValidationFailures(t *testing.T) {
	base := Values{DBUser: "local", DBName: "mealbot"}
	tests := []struct {
		name   string
		mutate func(*Values)
		want   string
	}{
		{name: "negative port", mutate: func(values *Values) { values.Port = -1 }, want: "PORT"},
		{name: "negative request timeout", mutate: func(values *Values) { values.RequestTimeout = -1 }, want: "REQUEST_TIMEOUT"},
		{name: "negative jwks timeout", mutate: func(values *Values) { values.JWKSTimeout = -1 }, want: "JWKS_TIMEOUT"},
		{name: "negative mail timeout", mutate: func(values *Values) { values.MailTimeout = -1 }, want: "MAIL_TIMEOUT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := base
			test.mutate(&values)
			_, err := New(values)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("New() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestFromMapRejectsPartialProviderGroups(t *testing.T) {
	tests := []struct {
		name    string
		entries []struct{ key, value string }
		want    string
	}{
		{
			name: "auth0",
			entries: []struct{ key, value string }{
				{"AUTH0_ISSUER", "https://tenant.example.test/"},
				{"AUTH0_AUDIENCE", "https://api.example.test/"},
				{"AUTH0_JWKS_URL", "https://tenant.example.test/.well-known/jwks.json"},
			},
			want: "AUTH0_ISSUER, AUTH0_AUDIENCE, and AUTH0_JWKS_URL must be set together",
		},
		{
			name: "mailgun",
			entries: []struct{ key, value string }{
				{"MAILGUN_SMTP_LOGIN", "mail@example.test"},
				{"MAILGUN_DOMAIN", "mg.example.test"},
				{"MAILGUN_API_KEY", "test-key"},
			},
			want: "MAILGUN_SMTP_LOGIN, MAILGUN_DOMAIN, and MAILGUN_API_KEY must be set together",
		},
	}

	for _, test := range tests {
		for mask := 1; mask < (1<<len(test.entries))-1; mask++ {
			values := map[string]string{"DB_USER": "local-user", "DB_NAME": "local-name"}
			var supplied []string
			for index, entry := range test.entries {
				if mask&(1<<index) != 0 {
					values[entry.key] = entry.value
					supplied = append(supplied, entry.key)
				}
			}
			t.Run(test.name+"/"+strings.Join(supplied, "+"), func(t *testing.T) {
				_, err := FromMap(values)
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("FromMap() error = %v, want containing %q", err, test.want)
				}
			})
		}
	}
}

func TestFromMapRejectsPartialAuth0GroupBeforeURLValidation(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
	}{
		{
			name: "malformed issuer",
			values: map[string]string{
				"AUTH0_ISSUER": "tenant.example.test",
			},
		},
		{
			name: "malformed JWKS URL",
			values: map[string]string{
				"AUTH0_JWKS_URL": "file:///keys.json",
			},
		},
	}

	const want = "AUTH0_ISSUER, AUTH0_AUDIENCE, and AUTH0_JWKS_URL must be set together"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.values["DB_USER"] = "local-user"
			test.values["DB_NAME"] = "local-name"
			_, err := FromMap(test.values)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("FromMap() error = %v, want containing %q", err, want)
			}
		})
	}
}

func TestProductionRequiresProviderConfiguration(t *testing.T) {
	required := []string{
		"AUTH0_ISSUER",
		"AUTH0_AUDIENCE",
		"AUTH0_JWKS_URL",
		"MAILGUN_SMTP_LOGIN",
		"MAILGUN_DOMAIN",
		"MAILGUN_API_KEY",
	}
	for _, key := range required {
		t.Run(key, func(t *testing.T) {
			values := completeProductionMap()
			delete(values, key)
			_, err := FromMap(values)
			if err == nil || !strings.Contains(err.Error(), key+" is required in production") {
				t.Fatalf("FromMap() error = %v", err)
			}
		})
	}
}

func completeProductionMap() map[string]string {
	return map[string]string{
		"APP_ENV":              "production",
		"PORT":                 "9443",
		"DATABASE_URL":         "postgresql://db.example.test/mealbot",
		"AUTH0_ISSUER":         "https://tenant.example.test/",
		"AUTH0_AUDIENCE":       "https://api.example.test",
		"AUTH0_JWKS_URL":       "https://tenant.example.test/.well-known/jwks.json",
		"MAILGUN_SMTP_LOGIN":   "mail@example.test",
		"MAILGUN_DOMAIN":       "mg.example.test",
		"MAILGUN_API_KEY":      "test-key",
		"STATIC_DIR":           "/srv/static",
		"CORS_ALLOWED_ORIGINS": "https://app.example.test,http://localhost:3000",
		"REQUEST_TIMEOUT":      "20s",
		"JWKS_TIMEOUT":         "3s",
		"MAIL_TIMEOUT":         "7s",
	}
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("strings = %#v, want %#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("strings[%d] = %q, want %q", index, got[index], want[index])
		}
	}
}
