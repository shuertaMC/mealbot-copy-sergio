package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// KeySource resolves an RSA verification key by its JWT key identifier. An
// implementation may cache keys, but it must honor context cancellation.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// Validator verifies RS256 signatures and the configured issuer and audience.
// Validator is safe for concurrent use when its KeySource is safe.
type Validator struct {
	issuer   string
	audience string
	keys     KeySource
	parser   *jwt.Parser
}

// NewValidator constructs a Validator. Issuer and audience are required, as
// is a key source; invalid construction parameters are reported immediately.
func NewValidator(issuer, audience string, keys KeySource) (*Validator, error) {
	issuer = strings.TrimSpace(issuer)
	audience = strings.TrimSpace(audience)
	if issuer == "" {
		return nil, errors.New("auth: issuer is required")
	}
	if audience == "" {
		return nil, errors.New("auth: audience is required")
	}
	if keys == nil {
		return nil, errors.New("auth: key source is required")
	}

	return &Validator{
		issuer:   issuer,
		audience: audience,
		keys:     keys,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
			jwt.WithIssuer(issuer),
			jwt.WithAudience(audience),
		),
	}, nil
}

// Validate verifies one compact JWT. All failures are normalized to
// ErrInvalidToken so callers cannot expose parser or provider internals.
func (v *Validator) Validate(ctx context.Context, tokenString string) error {
	if v == nil || v.parser == nil || strings.TrimSpace(tokenString) == "" {
		return ErrInvalidToken
	}

	claims := &jwt.RegisteredClaims{}
	token, err := v.parser.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		// Repeat the allow-list check here so the algorithm is rejected at the
		// consumer boundary before a key source can ever be consulted.
		if token.Method != jwt.SigningMethodRS256 || token.Header["alg"] != jwt.SigningMethodRS256.Alg() {
			return nil, ErrInvalidToken
		}
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" || strings.TrimSpace(kid) != kid {
			return nil, ErrInvalidToken
		}
		return v.keys.Key(ctx, kid)
	})
	if err != nil || token == nil || !token.Valid {
		return ErrInvalidToken
	}
	return nil
}
