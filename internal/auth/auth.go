package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/server"
)

// InvalidAccessToken is the message returned to clients that fail authentication.
const InvalidAccessToken = "Invalid access token"

// Validator checks that a bearer token is a currently valid RS256 JWT for the
// configured issuer and audience, signed by a key published in the JWKS.
type Validator struct {
	parser *jwt.Parser
	keys   KeyProvider
	logger *slog.Logger
}

// NewValidator builds a Validator from the Auth0 settings. keys supplies signing keys.
func NewValidator(cfg config.Auth0, keys KeyProvider, logger *slog.Logger) *Validator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Validator{
		parser: jwt.NewParser(
			// Only accept the expected signing algorithm; see
			// https://auth0.com/blog/2015/03/31/critical-vulnerabilities-in-json-web-token-libraries/
			jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Audience),
		),
		keys:   keys,
		logger: logger,
	}
}

// Validate parses and verifies tokenString.
func (v *Validator) Validate(ctx context.Context, tokenString string) (*jwt.Token, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := v.parser.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (interface{}, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("token header has no kid")
		}
		key, err := v.keys.Key(ctx, kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token is invalid")
	}
	return token, nil
}

// Handler wraps next so that it only runs for requests carrying a valid token.
// Preflight (OPTIONS) requests pass through untouched. Failures are answered
// with 401 Unauthorized.
func (v *Validator) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		tokenString, err := bearerToken(r)
		if err == nil {
			_, err = v.Validate(r.Context(), tokenString)
		}
		if err != nil {
			v.logger.Warn("authentication failed", "method", r.Method, "path", r.URL.Path, "error", err)
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(server.StrToBytes(InvalidAccessToken))
			return
		}

		next.ServeHTTP(w, r)
	})
}

func bearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", errors.New("no authorization header")
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", errors.New("authorization header format must be Bearer {token}")
	}
	return parts[1], nil
}
