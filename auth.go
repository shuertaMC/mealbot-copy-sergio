package mealbot

import (
	"net/http"
	"sync"

	internalauth "github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
)

const (
	// InvalidAccessToken is retained for compatibility with the legacy package.
	InvalidAccessToken = internalauth.InvalidAccessToken
	// Issuer is the documented rollout default for the Auth0 issuer.
	Issuer = config.DefaultAuthIssuer
	// Audience is the documented rollout default for the Auth0 audience.
	Audience = config.DefaultAudience
	// JSONWebKeySet is the documented rollout default JWKS endpoint.
	JSONWebKeySet = config.DefaultJWKSURL
)

// CustomJWTMiddleware is a temporary compatibility adapter for the legacy HTTP
// transport. Authentication failures intentionally keep the legacy empty
// response until handlers move to internal/httpapi.
type CustomJWTMiddleware struct {
	validator *internalauth.Validator
}

// Handler protects h while preserving the legacy transport response contract.
func (mw *CustomJWTMiddleware) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := mw.CheckJWT(w, r); err != nil {
			return
		}
		h.ServeHTTP(w, r)
	})
}

// CheckJWT validates the request credential. The ResponseWriter parameter is
// retained only for source compatibility with the legacy method.
func (mw *CustomJWTMiddleware) CheckJWT(_ http.ResponseWriter, r *http.Request) error {
	if r.Method == http.MethodOptions {
		return nil
	}
	token, err := internalauth.BearerToken(r.Header.Get("Authorization"))
	if err != nil || mw == nil || mw.validator == nil {
		return internalauth.ErrUnauthorized
	}
	return mw.validator.Validate(r.Context(), token)
}

// GetFakeAuthHandler returns a handler without authentication for compatibility
// characterization tests.
func GetFakeAuthHandler(handler http.Handler) http.Handler {
	return handler
}

// NewAuthHandler constructs the legacy-shaped adapter from injected immutable
// configuration and an injected HTTP client. It performs no environment reads.
func NewAuthHandler(cfg config.Config, client *http.Client) (func(http.Handler) http.Handler, error) {
	provider, err := internalauth.NewJWKSProvider(internalauth.JWKSConfig{
		URL:    cfg.AuthJWKSURL,
		Client: client,
	})
	if err != nil {
		return nil, internalauth.ErrUnauthorized
	}
	validator, err := internalauth.NewValidator(internalauth.ValidatorConfig{
		Issuer:   cfg.AuthIssuer,
		Audience: cfg.AuthAudience,
		Keys:     provider,
	})
	if err != nil {
		return nil, internalauth.ErrUnauthorized
	}
	middleware := &CustomJWTMiddleware{validator: validator}
	return middleware.Handler, nil
}

var (
	defaultAuthOnce sync.Once
	defaultAuth     func(http.Handler) http.Handler
)

// GetAuthHandler retains the legacy constructor using documented rollout
// defaults. All routes share one concurrency-safe JWKS cache.
func GetAuthHandler(handler http.Handler) http.Handler {
	defaultAuthOnce.Do(func() {
		configured, err := NewAuthHandler(config.Config{
			AuthIssuer:   Issuer,
			AuthAudience: Audience,
			AuthJWKSURL:  JSONWebKeySet,
		}, nil)
		if err == nil {
			defaultAuth = configured
		}
	})
	if defaultAuth == nil {
		return (&CustomJWTMiddleware{}).Handler(handler)
	}
	return defaultAuth(handler)
}
