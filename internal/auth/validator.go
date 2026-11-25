package auth

import (
	"context"
	"crypto/rsa"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// KeyProvider supplies RSA verification keys by JWT key ID.
type KeyProvider interface {
	Key(context.Context, string) (*rsa.PublicKey, error)
}

// ValidatorConfig contains all validator dependencies. It is intentionally
// independent of process environment state.
type ValidatorConfig struct {
	Issuer   string
	Audience string
	Keys     KeyProvider
	Now      func() time.Time
}

// Validator validates signed Auth0 access tokens.
type Validator struct {
	keys   KeyProvider
	parser *jwt.Parser
}

// NewValidator builds a validator pinned to RS256 and the configured issuer
// and audience. Configuration failures use the same non-sensitive error as
// request failures because auth configuration values may themselves be secret.
func NewValidator(cfg ValidatorConfig) (*Validator, error) {
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.Audience) == "" || cfg.Keys == nil {
		return nil, ErrUnauthorized
	}

	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithJSONNumber(),
	}
	if cfg.Now != nil {
		options = append(options, jwt.WithTimeFunc(cfg.Now))
	}

	return &Validator{
		keys:   cfg.Keys,
		parser: jwt.NewParser(options...),
	}, nil
}

// Validate verifies token structure, exact signing algorithm, key ID,
// signature, issuer, audience, and jwt/v5's default temporal claims.
func (v *Validator) Validate(ctx context.Context, raw string) error {
	if v == nil || v.keys == nil || v.parser == nil || strings.TrimSpace(raw) == "" {
		return ErrUnauthorized
	}
	if ctx == nil {
		ctx = context.Background()
	}

	token, err := v.parser.ParseWithClaims(raw, jwt.MapClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodRS256 || token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, ErrUnauthorized
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || strings.TrimSpace(kid) == "" {
			return nil, ErrUnauthorized
		}
		key, err := v.keys.Key(ctx, kid)
		if err != nil || key == nil {
			return nil, ErrUnauthorized
		}
		return key, nil
	})
	if err != nil || token == nil || !token.Valid {
		return ErrUnauthorized
	}
	return nil
}
