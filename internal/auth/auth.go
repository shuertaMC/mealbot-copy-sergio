// Package auth validates Auth0 issued JSON Web Tokens.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

const (
	// InvalidAccessToken : Message to show if invalid access is invalid
	InvalidAccessToken = "Invalid access token"
	// SigningAlgorithm is the only accepted token signing algorithm.
	// Important to avoid the security issues described here:
	// https://auth0.com/blog/2015/03/31/critical-vulnerabilities-in-json-web-token-libraries/
	SigningAlgorithm = "RS256"
)

// Config describes what tokens are accepted.
type Config struct {
	// Issuer : Value of the 'iss' claim in a JSON Web Token https://jwt.io/introduction/
	Issuer string
	// Audience : Value of the 'aud' claim in a JSON Web Token
	Audience string
	// JWKSURL : Location of the key set containing the public keys used for
	// verifying tokens https://auth0.com/docs/jwks
	JWKSURL string
}

// Middleware is HTTP middleware with JWT authentication capabilities.
type Middleware struct {
	cfg    Config
	ctx    context.Context
	parser *jwt.Parser

	mu sync.Mutex
	kf keyfunc.Keyfunc
}

// NewMiddleware creates the middleware. The JWKS is fetched lazily on the first
// authenticated request (and then cached and refreshed in the background by
// keyfunc), so constructing the middleware never touches the network. ctx
// bounds the lifetime of the background refresh.
func NewMiddleware(ctx context.Context, cfg Config) *Middleware {
	return &Middleware{
		cfg: cfg,
		ctx: ctx,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{SigningAlgorithm}),
			jwt.WithIssuer(cfg.Issuer),
			jwt.WithAudience(cfg.Audience),
			jwt.WithExpirationRequired(),
		),
	}
}

func (m *Middleware) keyfunc() (keyfunc.Keyfunc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.kf != nil {
		return m.kf, nil
	}

	kf, err := keyfunc.NewDefaultCtx(m.ctx, []string{m.cfg.JWKSURL})
	if err != nil {
		return nil, err
	}
	m.kf = kf
	return kf, nil
}

// Handler : Serve the wrapped handler only if the request carries a valid JWT;
// else respond with 401 Unauthorized.
func (m *Middleware) Handler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := m.CheckJWT(r)
		if err != nil {
			slog.Warn("authentication failed", "error", err, "path", r.URL.Path)
			writeUnauthorized(w, err)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// CheckJWT : Validate JWT sent to server
func (m *Middleware) CheckJWT(r *http.Request) error {
	// preflight request
	if r.Method == http.MethodOptions {
		return nil
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return errors.New("No authorization header")
	}

	authStr := strings.Split(authHeader, " ")
	if len(authStr) != 2 || strings.ToLower(authStr[0]) != "bearer" {
		return errors.New("Authorization header format must be Bearer {token}")
	}

	kf, err := m.keyfunc()
	if err != nil {
		return errors.New("Unable to load signing keys: " + err.Error())
	}

	parsedToken, err := m.parser.Parse(authStr[1], kf.Keyfunc)
	if err != nil {
		return err
	}

	if !parsedToken.Valid {
		return errors.New("Token is invalid")
	}

	return nil
}

func writeUnauthorized(w http.ResponseWriter, err error) {
	body, _ := json.Marshal(struct{ Message string }{err.Error()})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write(body)
}
