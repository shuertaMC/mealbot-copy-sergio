// Package config loads and validates Mealbot's process configuration.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort              = 8080
	defaultStaticDir         = "static"
	defaultRequestTimeout    = 15 * time.Second
	defaultJWKSTimeout       = 5 * time.Second
	defaultMailTimeout       = 10 * time.Second
	defaultPairingClaimLimit = 100
	defaultPairingClaimLease = 15 * time.Minute
	defaultAuth0Issuer       = "https://mealbot.auth0.com/"
	defaultAuth0Audience     = "https://mealbot-2.herokuapp.com/"
	defaultAuth0JWKSURL      = "https://mealbot.auth0.com/.well-known/jwks.json"
)

// Mode identifies the validation policy applied to a configuration.
type Mode string

const (
	// Development permits provider settings to be absent so local tests and
	// static-file development do not require credentials.
	Development Mode = "development"
	// Production requires every external integration setting.
	Production Mode = "production"
)

// LookupFunc retrieves a value by environment-style key.
type LookupFunc func(string) (string, bool)

// Values is the typed input accepted by New. Zero values receive documented
// development defaults, except database selection, which is always explicit.
type Values struct {
	Mode               Mode
	Port               int
	DatabaseURL        string
	DBUser             string
	DBName             string
	Auth0Issuer        string
	Auth0Audience      string
	Auth0JWKSURL       string
	MailgunSMTPLogin   string
	MailgunDomain      string
	MailgunAPIKey      string
	StaticDir          string
	AllowedCORSOrigins []string
	RequestTimeout     time.Duration
	JWKSTimeout        time.Duration
	MailTimeout        time.Duration
	PairingClaimLimit  int
	PairingClaimLease  time.Duration
}

// Database contains one validated database connection selection. URL is set
// for hosted connections; User and Name are set for local connections.
type Database struct {
	URL  string
	User string
	Name string
}

// Auth0 contains the settings used to validate Auth0 access tokens.
type Auth0 struct {
	Issuer   string
	Audience string
	JWKSURL  string
}

// Mailgun contains the settings used to send pairing messages.
type Mailgun struct {
	SMTPLogin string
	Domain    string
	APIKey    string
}

// Config is an immutable, validated application configuration. Its accessors
// return values or defensive copies, so callers cannot mutate shared state.
type Config struct {
	mode              Mode
	port              int
	database          Database
	auth0             Auth0
	mailgun           Mailgun
	staticDir         string
	allowedOrigins    []string
	requestTimeout    time.Duration
	jwksTimeout       time.Duration
	mailTimeout       time.Duration
	pairingClaimLimit int
	pairingClaimLease time.Duration
}

// New validates typed values and returns an immutable Config. It applies safe,
// non-secret development defaults for scalar settings and Auth0.
func New(values Values) (Config, error) {
	values = withDefaults(values)
	values = trimValues(values)
	values = withDevelopmentAuth0Defaults(values)

	if err := validateValues(values); err != nil {
		return Config{}, err
	}

	database := Database{URL: values.DatabaseURL, User: values.DBUser, Name: values.DBName}
	return Config{
		mode:              values.Mode,
		port:              values.Port,
		database:          database,
		auth0:             Auth0{Issuer: values.Auth0Issuer, Audience: values.Auth0Audience, JWKSURL: values.Auth0JWKSURL},
		mailgun:           Mailgun{SMTPLogin: values.MailgunSMTPLogin, Domain: values.MailgunDomain, APIKey: values.MailgunAPIKey},
		staticDir:         values.StaticDir,
		allowedOrigins:    append([]string(nil), values.AllowedCORSOrigins...),
		requestTimeout:    values.RequestTimeout,
		jwksTimeout:       values.JWKSTimeout,
		mailTimeout:       values.MailTimeout,
		pairingClaimLimit: values.PairingClaimLimit,
		pairingClaimLease: values.PairingClaimLease,
	}, nil
}

// Load reads the process environment once through os.LookupEnv and validates
// the resulting configuration. It does not retain or mutate global state.
func Load() (Config, error) {
	return FromLookup(os.LookupEnv)
}

// FromMap loads configuration from a map. The map is only read during the
// call, making it suitable for deterministic tests.
func FromMap(values map[string]string) (Config, error) {
	return FromLookup(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
}

// FromLookup loads and validates configuration using an injected lookup
// function. A nil lookup is rejected.
func FromLookup(lookup LookupFunc) (Config, error) {
	if lookup == nil {
		return Config{}, errors.New("config: lookup function is nil")
	}

	port, err := parseInt(lookup, "PORT", defaultPort)
	if err != nil {
		return Config{}, err
	}
	if err := validatePort(port); err != nil {
		return Config{}, err
	}
	pairingClaimLimit, err := parseInt(lookup, "PAIRING_CLAIM_LIMIT", defaultPairingClaimLimit)
	if err != nil {
		return Config{}, err
	}
	if err := validatePositiveInt("PAIRING_CLAIM_LIMIT", pairingClaimLimit); err != nil {
		return Config{}, err
	}
	pairingClaimLease, err := parseDuration(lookup, "PAIRING_CLAIM_LEASE", defaultPairingClaimLease)
	if err != nil {
		return Config{}, err
	}
	if err := validatePositiveDuration("PAIRING_CLAIM_LEASE", pairingClaimLease); err != nil {
		return Config{}, err
	}
	requestTimeout, err := parseDuration(lookup, "REQUEST_TIMEOUT", defaultRequestTimeout)
	if err != nil {
		return Config{}, err
	}
	if err := validatePositiveDuration("REQUEST_TIMEOUT", requestTimeout); err != nil {
		return Config{}, err
	}
	jwksTimeout, err := parseDuration(lookup, "JWKS_TIMEOUT", defaultJWKSTimeout)
	if err != nil {
		return Config{}, err
	}
	if err := validatePositiveDuration("JWKS_TIMEOUT", jwksTimeout); err != nil {
		return Config{}, err
	}
	mailTimeout, err := parseDuration(lookup, "MAIL_TIMEOUT", defaultMailTimeout)
	if err != nil {
		return Config{}, err
	}
	if err := validatePositiveDuration("MAIL_TIMEOUT", mailTimeout); err != nil {
		return Config{}, err
	}

	mode := Mode(valueOrDefault(lookup, "APP_ENV", string(Development)))
	origins := splitList(valueOrDefault(lookup, "CORS_ALLOWED_ORIGINS", ""))
	return New(Values{
		Mode:               mode,
		Port:               port,
		DatabaseURL:        value(lookup, "DATABASE_URL"),
		DBUser:             value(lookup, "DB_USER"),
		DBName:             value(lookup, "DB_NAME"),
		Auth0Issuer:        value(lookup, "AUTH0_ISSUER"),
		Auth0Audience:      value(lookup, "AUTH0_AUDIENCE"),
		Auth0JWKSURL:       value(lookup, "AUTH0_JWKS_URL"),
		MailgunSMTPLogin:   value(lookup, "MAILGUN_SMTP_LOGIN"),
		MailgunDomain:      value(lookup, "MAILGUN_DOMAIN"),
		MailgunAPIKey:      value(lookup, "MAILGUN_API_KEY"),
		StaticDir:          valueOrDefault(lookup, "STATIC_DIR", defaultStaticDir),
		AllowedCORSOrigins: origins,
		RequestTimeout:     requestTimeout,
		JWKSTimeout:        jwksTimeout,
		MailTimeout:        mailTimeout,
		PairingClaimLimit:  pairingClaimLimit,
		PairingClaimLease:  pairingClaimLease,
	})
}

// Mode returns the configured operating mode.
func (c Config) Mode() Mode { return c.mode }

// Port returns the validated TCP listen port.
func (c Config) Port() int { return c.port }

// Database returns the validated database selection.
func (c Config) Database() Database { return c.database }

// Auth0 returns the token validation settings.
func (c Config) Auth0() Auth0 { return c.auth0 }

// Mailgun returns the mail delivery settings.
func (c Config) Mailgun() Mailgun { return c.mailgun }

// StaticDir returns the directory served by the unauthenticated static route.
func (c Config) StaticDir() string { return c.staticDir }

// AllowedCORSOrigins returns a defensive copy of the configured origins.
func (c Config) AllowedCORSOrigins() []string {
	return append([]string(nil), c.allowedOrigins...)
}

// RequestTimeout returns the overall inbound request timeout.
func (c Config) RequestTimeout() time.Duration { return c.requestTimeout }

// JWKSTimeout returns the timeout for remote key retrieval.
func (c Config) JWKSTimeout() time.Duration { return c.jwksTimeout }

// MailTimeout returns the timeout for one mail delivery call.
func (c Config) MailTimeout() time.Duration { return c.mailTimeout }

// PairingClaimLimit returns the maximum number of due rounds leased by one scheduler invocation.
func (c Config) PairingClaimLimit() int { return c.pairingClaimLimit }

// PairingClaimLease returns how long an unfinished round claim remains exclusive before retry.
func (c Config) PairingClaimLease() time.Duration { return c.pairingClaimLease }

func withDefaults(values Values) Values {
	if values.Mode == "" {
		values.Mode = Development
	}
	if values.Port == 0 {
		values.Port = defaultPort
	}
	if values.StaticDir == "" {
		values.StaticDir = defaultStaticDir
	}
	if values.RequestTimeout == 0 {
		values.RequestTimeout = defaultRequestTimeout
	}
	if values.JWKSTimeout == 0 {
		values.JWKSTimeout = defaultJWKSTimeout
	}
	if values.MailTimeout == 0 {
		values.MailTimeout = defaultMailTimeout
	}
	if values.PairingClaimLimit == 0 {
		values.PairingClaimLimit = defaultPairingClaimLimit
	}
	if values.PairingClaimLease == 0 {
		values.PairingClaimLease = defaultPairingClaimLease
	}
	return values
}

func trimValues(values Values) Values {
	values.Mode = Mode(strings.ToLower(strings.TrimSpace(string(values.Mode))))
	values.DatabaseURL = strings.TrimSpace(values.DatabaseURL)
	values.DBUser = strings.TrimSpace(values.DBUser)
	values.DBName = strings.TrimSpace(values.DBName)
	values.Auth0Issuer = strings.TrimSpace(values.Auth0Issuer)
	values.Auth0Audience = strings.TrimSpace(values.Auth0Audience)
	values.Auth0JWKSURL = strings.TrimSpace(values.Auth0JWKSURL)
	values.MailgunSMTPLogin = strings.TrimSpace(values.MailgunSMTPLogin)
	values.MailgunDomain = strings.TrimSpace(values.MailgunDomain)
	values.MailgunAPIKey = strings.TrimSpace(values.MailgunAPIKey)
	values.StaticDir = strings.TrimSpace(values.StaticDir)
	for index := range values.AllowedCORSOrigins {
		values.AllowedCORSOrigins[index] = strings.TrimSuffix(strings.TrimSpace(values.AllowedCORSOrigins[index]), "/")
	}
	return values
}

func withDevelopmentAuth0Defaults(values Values) Values {
	if values.Mode == Development && values.Auth0Issuer == "" && values.Auth0Audience == "" && values.Auth0JWKSURL == "" {
		values.Auth0Issuer = defaultAuth0Issuer
		values.Auth0Audience = defaultAuth0Audience
		values.Auth0JWKSURL = defaultAuth0JWKSURL
	}
	return values
}

func validateValues(values Values) error {
	if values.Mode != Development && values.Mode != Production {
		return fmt.Errorf("config: APP_ENV must be %q or %q", Development, Production)
	}
	if err := validatePort(values.Port); err != nil {
		return err
	}
	if values.StaticDir == "" {
		return errors.New("config: STATIC_DIR must not be empty")
	}
	if err := validatePositiveDuration("REQUEST_TIMEOUT", values.RequestTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("JWKS_TIMEOUT", values.JWKSTimeout); err != nil {
		return err
	}
	if err := validatePositiveDuration("MAIL_TIMEOUT", values.MailTimeout); err != nil {
		return err
	}
	if err := validatePositiveInt("PAIRING_CLAIM_LIMIT", values.PairingClaimLimit); err != nil {
		return err
	}
	if err := validatePositiveDuration("PAIRING_CLAIM_LEASE", values.PairingClaimLease); err != nil {
		return err
	}
	if err := validateDatabase(values); err != nil {
		return err
	}
	if values.Mode == Development {
		if err := validateDevelopmentProviders(values); err != nil {
			return err
		}
	}
	if values.Auth0Issuer != "" {
		if err := validateHTTPURL("AUTH0_ISSUER", values.Auth0Issuer); err != nil {
			return err
		}
	}
	if values.Auth0JWKSURL != "" {
		if err := validateHTTPURL("AUTH0_JWKS_URL", values.Auth0JWKSURL); err != nil {
			return err
		}
	}
	if err := validateOrigins(values.AllowedCORSOrigins); err != nil {
		return err
	}
	if values.Mode == Production {
		return validateProduction(values)
	}
	return nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return errors.New("config: PORT must be between 1 and 65535")
	}
	return nil
}

func validatePositiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("config: %s must be positive", name)
	}
	return nil
}

func validatePositiveInt(name string, value int) error {
	if value <= 0 {
		return fmt.Errorf("config: %s must be positive", name)
	}
	return nil
}

func validateDatabase(values Values) error {
	hasURL := values.DatabaseURL != ""
	hasUser := values.DBUser != ""
	hasName := values.DBName != ""
	if hasURL && (hasUser || hasName) {
		return errors.New("config: set DATABASE_URL or DB_USER and DB_NAME, not both")
	}
	if !hasURL && (!hasUser || !hasName) {
		return errors.New("config: DATABASE_URL or both DB_USER and DB_NAME are required")
	}
	if !hasURL {
		return nil
	}
	parsed, err := url.Parse(values.DatabaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("config: DATABASE_URL must be an absolute PostgreSQL URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return errors.New("config: DATABASE_URL must use postgres or postgresql scheme")
	}
	if parsed.Path == "" || parsed.Path == "/" {
		return errors.New("config: DATABASE_URL must select a database in its URL path")
	}
	return nil
}

func validateProduction(values Values) error {
	required := []struct {
		name  string
		value string
	}{
		{"AUTH0_ISSUER", values.Auth0Issuer},
		{"AUTH0_AUDIENCE", values.Auth0Audience},
		{"AUTH0_JWKS_URL", values.Auth0JWKSURL},
		{"MAILGUN_SMTP_LOGIN", values.MailgunSMTPLogin},
		{"MAILGUN_DOMAIN", values.MailgunDomain},
		{"MAILGUN_API_KEY", values.MailgunAPIKey},
	}
	for _, setting := range required {
		if setting.value == "" {
			return fmt.Errorf("config: %s is required in production", setting.name)
		}
	}
	return nil
}

func validateDevelopmentProviders(values Values) error {
	auth0Count := populatedCount(values.Auth0Issuer, values.Auth0Audience, values.Auth0JWKSURL)
	if auth0Count != 3 {
		return errors.New("config: AUTH0_ISSUER, AUTH0_AUDIENCE, and AUTH0_JWKS_URL must be set together")
	}
	mailgunCount := populatedCount(values.MailgunSMTPLogin, values.MailgunDomain, values.MailgunAPIKey)
	if mailgunCount != 0 && mailgunCount != 3 {
		return errors.New("config: MAILGUN_SMTP_LOGIN, MAILGUN_DOMAIN, and MAILGUN_API_KEY must be set together")
	}
	return nil
}

func populatedCount(values ...string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func validateHTTPURL(name, rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("config: %s must be an absolute HTTP(S) URL", name)
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("config: %s must not contain user information or a fragment", name)
	}
	return nil
}

func validateOrigins(origins []string) error {
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		if origin == "" {
			return errors.New("config: CORS_ALLOWED_ORIGINS contains an empty origin")
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("config: invalid CORS origin %q", origin)
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return fmt.Errorf("config: CORS origin %q must contain only scheme and host", origin)
		}
		if host := parsed.Hostname(); host == "" || (net.ParseIP(host) == nil && strings.ContainsAny(host, " /")) {
			return fmt.Errorf("config: invalid CORS origin %q", origin)
		}
		if _, exists := seen[origin]; exists {
			return fmt.Errorf("config: duplicate CORS origin %q", origin)
		}
		seen[origin] = struct{}{}
	}
	return nil
}

func value(lookup LookupFunc, key string) string {
	value, _ := lookup(key)
	return value
}

func valueOrDefault(lookup LookupFunc, key, fallback string) string {
	value, ok := lookup(key)
	if !ok {
		return fallback
	}
	return value
}

func parseInt(lookup LookupFunc, key string, fallback int) (int, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("config: %s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func parseDuration(lookup LookupFunc, key string, fallback time.Duration) (time.Duration, error) {
	raw, ok := lookup(key)
	if !ok {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("config: %s must be a duration: %w", key, err)
	}
	return parsed, nil
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return strings.Split(raw, ",")
}
