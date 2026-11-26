package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxJWKSBodyBytes int64 = 1 << 20

// JWKSKeySource retrieves RSA signing keys from a configured HTTPS or HTTP
// endpoint and atomically caches each complete successful key set. Failed
// refreshes never replace a usable cache.
type JWKSKeySource struct {
	client  *http.Client
	url     string
	timeout time.Duration

	mu         sync.RWMutex
	keys       map[string]*rsa.PublicKey
	generation uint64
	refreshMu  sync.Mutex
}

// NewJWKSKeySource validates its dependencies and constructs a concurrent,
// context-aware key source. The injected client is not mutated.
func NewJWKSKeySource(client *http.Client, rawURL string, timeout time.Duration) (*JWKSKeySource, error) {
	if client == nil {
		return nil, errors.New("auth: HTTP client is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("auth: valid JWKS URL is required")
	}
	if timeout <= 0 {
		return nil, errors.New("auth: JWKS timeout must be positive")
	}
	return &JWKSKeySource{
		client:  client,
		url:     parsed.String(),
		timeout: timeout,
		keys:    make(map[string]*rsa.PublicKey),
	}, nil
}

// Key returns a cached key when possible. A cache miss triggers one serialized
// refresh; the requested key is looked up again after that refresh.
func (s *JWKSKeySource) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if s == nil || strings.TrimSpace(kid) == "" || strings.TrimSpace(kid) != kid {
		return nil, ErrKeyNotFound
	}
	key, observedGeneration := s.cached(kid)
	if key != nil {
		return key, nil
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	key, currentGeneration := s.cached(kid)
	if key != nil {
		return key, nil
	}
	// Another caller already refreshed the generation observed by this
	// lookup. Do not turn a concurrent unknown kid into a request stampede.
	if currentGeneration != observedGeneration {
		return nil, ErrKeyNotFound
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("%w: request canceled", ErrInvalidJWKS)
	}
	keys, err := s.fetch(ctx)
	s.mu.Lock()
	s.generation++
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.keys = keys
	key = s.keys[kid]
	s.mu.Unlock()
	if key == nil {
		return nil, ErrKeyNotFound
	}
	return key, nil
}

func (s *JWKSKeySource) cached(kid string) (*rsa.PublicKey, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.keys[kid], s.generation
}

func (s *JWKSKeySource) fetch(parent context.Context) (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: create request", ErrInvalidJWKS)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed: %v", ErrInvalidJWKS, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: unexpected HTTP status", ErrInvalidJWKS)
	}

	limited := io.LimitReader(resp.Body, maxJWKSBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%w: read response", ErrInvalidJWKS)
	}
	if int64(len(body)) > maxJWKSBodyBytes {
		return nil, fmt.Errorf("%w: response too large", ErrInvalidJWKS)
	}
	return parseJWKS(body)
}

type jwksDocument struct {
	Keys []jwkDocument `json:"keys"`
}

type jwkDocument struct {
	KTY string          `json:"kty"`
	KID string          `json:"kid"`
	Use string          `json:"use"`
	Alg string          `json:"alg"`
	N   string          `json:"n"`
	E   string          `json:"e"`
	X5C json.RawMessage `json:"x5c"`
}

func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var document jwksDocument
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: malformed JSON", ErrInvalidJWKS)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing JSON", ErrInvalidJWKS)
	}
	if len(document.Keys) == 0 {
		return nil, fmt.Errorf("%w: no keys", ErrInvalidJWKS)
	}

	keys := make(map[string]*rsa.PublicKey, len(document.Keys))
	for _, item := range document.Keys {
		if item.KID == "" || strings.TrimSpace(item.KID) != item.KID {
			return nil, fmt.Errorf("%w: missing key identifier", ErrInvalidJWKS)
		}
		if _, duplicate := keys[item.KID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate key identifier", ErrInvalidJWKS)
		}
		if item.KTY != "RSA" || item.Use != "sig" || (item.Alg != "" && item.Alg != jwtRS256) {
			return nil, fmt.Errorf("%w: unsupported key metadata", ErrInvalidJWKS)
		}

		key, err := parseJWK(item)
		if err != nil {
			return nil, err
		}
		keys[item.KID] = key
	}
	return keys, nil
}

const jwtRS256 = "RS256"

func parseJWK(item jwkDocument) (*rsa.PublicKey, error) {
	if item.X5C != nil {
		var certificates []string
		if err := json.Unmarshal(item.X5C, &certificates); err != nil || len(certificates) == 0 {
			return nil, fmt.Errorf("%w: malformed certificate chain", ErrInvalidJWKS)
		}
		var certificate *x509.Certificate
		for index, encoded := range certificates {
			der, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil || len(der) == 0 {
				return nil, fmt.Errorf("%w: malformed certificate", ErrInvalidJWKS)
			}
			parsed, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, fmt.Errorf("%w: malformed certificate", ErrInvalidJWKS)
			}
			if index == 0 {
				certificate = parsed
			}
		}
		key, ok := certificate.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("%w: certificate key is not RSA", ErrInvalidJWKS)
		}
		return key, nil
	}

	modulusBytes, err := base64.RawURLEncoding.DecodeString(item.N)
	if err != nil || len(modulusBytes) == 0 {
		return nil, fmt.Errorf("%w: malformed modulus", ErrInvalidJWKS)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(item.E)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 8 {
		return nil, fmt.Errorf("%w: malformed exponent", ErrInvalidJWKS)
	}
	exponentValue := new(big.Int).SetBytes(exponentBytes)
	if !exponentValue.IsInt64() {
		return nil, fmt.Errorf("%w: malformed exponent", ErrInvalidJWKS)
	}
	exponent := exponentValue.Int64()
	maxInt := int64(^uint(0) >> 1)
	if exponent < 3 || exponent > maxInt || exponent%2 == 0 {
		return nil, fmt.Errorf("%w: malformed exponent", ErrInvalidJWKS)
	}
	modulus := new(big.Int).SetBytes(modulusBytes)
	if modulus.Sign() <= 0 {
		return nil, fmt.Errorf("%w: malformed modulus", ErrInvalidJWKS)
	}
	return &rsa.PublicKey{N: modulus, E: int(exponent)}, nil
}
