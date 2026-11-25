package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultRequestTimeout = 5 * time.Second
	defaultMaxBodyBytes   = int64(1 << 20)
	defaultMaxKeys        = 128
	defaultMaxKeyIDLength = 256
	defaultMissingKeyTTL  = time.Minute
	defaultFailureRetry   = time.Second
	minRSAKeyBits         = 2048
	maxRSAModulusBytes    = 1024
)

var (
	// ErrKeyNotFound does not include the requested key ID.
	ErrKeyNotFound = errors.New("authentication key not found")
	// ErrProviderUnavailable hides network responses and malformed key data.
	ErrProviderUnavailable = errors.New("authentication key provider unavailable")
)

// JWKSConfig bounds every resource used to retrieve and cache verification
// keys. Zero values select conservative defaults.
type JWKSConfig struct {
	URL              string
	Client           *http.Client
	RequestTimeout   time.Duration
	MaxResponseBytes int64
	MaxKeys          int
	MaxKeyIDLength   int
	MissingKeyTTL    time.Duration
	FailureRetry     time.Duration
	Now              func() time.Time
}

// JWKSProvider caches an entire bounded JWKS and coalesces concurrent refreshes.
type JWKSProvider struct {
	url              string
	client           *http.Client
	requestTimeout   time.Duration
	maxResponseBytes int64
	maxKeys          int
	maxKeyIDLength   int
	missingKeyTTL    time.Duration
	failureRetry     time.Duration
	now              func() time.Time

	mu             sync.Mutex
	keys           map[string]rsa.PublicKey
	missing        map[string]time.Time
	refreshing     bool
	refreshDone    chan struct{}
	generation     uint64
	lastRefreshErr error
	retryAfter     time.Time
}

// NewJWKSProvider constructs a provider without performing network I/O.
func NewJWKSProvider(cfg JWKSConfig) (*JWKSProvider, error) {
	parsed, err := url.Parse(cfg.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, ErrProviderUnavailable
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{}
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = defaultRequestTimeout
	}
	if cfg.MaxResponseBytes <= 0 {
		cfg.MaxResponseBytes = defaultMaxBodyBytes
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = defaultMaxKeys
	}
	if cfg.MaxKeyIDLength <= 0 {
		cfg.MaxKeyIDLength = defaultMaxKeyIDLength
	}
	if cfg.MissingKeyTTL <= 0 {
		cfg.MissingKeyTTL = defaultMissingKeyTTL
	}
	if cfg.FailureRetry <= 0 {
		cfg.FailureRetry = defaultFailureRetry
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.MaxResponseBytes == int64(^uint64(0)>>1) {
		return nil, ErrProviderUnavailable
	}

	return &JWKSProvider{
		url:              cfg.URL,
		client:           cfg.Client,
		requestTimeout:   cfg.RequestTimeout,
		maxResponseBytes: cfg.MaxResponseBytes,
		maxKeys:          cfg.MaxKeys,
		maxKeyIDLength:   cfg.MaxKeyIDLength,
		missingKeyTTL:    cfg.MissingKeyTTL,
		failureRetry:     cfg.FailureRetry,
		now:              cfg.Now,
		keys:             make(map[string]rsa.PublicKey),
		missing:          make(map[string]time.Time),
	}, nil
}

// Key returns a cached key or performs one coalesced refresh for a key ID that
// has not been seen recently. A successful refresh atomically replaces the
// cache, which supports normal JWKS key rotation.
func (p *JWKSProvider) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if p == nil || strings.TrimSpace(kid) == "" || len(kid) > p.maxKeyIDLength {
		return nil, ErrKeyNotFound
	}
	if ctx == nil {
		ctx = context.Background()
	}

	p.mu.Lock()
	if key, ok := p.keys[kid]; ok {
		p.mu.Unlock()
		return clonePublicKey(key), nil
	}
	now := p.now()
	if now.Before(p.retryAfter) {
		p.mu.Unlock()
		return nil, ErrProviderUnavailable
	}
	if until, ok := p.missing[kid]; ok && now.Before(until) {
		p.mu.Unlock()
		return nil, ErrKeyNotFound
	}
	delete(p.missing, kid)

	if p.refreshing {
		done := p.refreshDone
		generation := p.generation
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ErrProviderUnavailable
		case <-done:
		}

		p.mu.Lock()
		defer p.mu.Unlock()
		if key, ok := p.keys[kid]; ok {
			return clonePublicKey(key), nil
		}
		if p.generation != generation && p.lastRefreshErr != nil {
			return nil, ErrProviderUnavailable
		}
		p.rememberMissingLocked(kid, p.now())
		return nil, ErrKeyNotFound
	}

	p.refreshing = true
	p.refreshDone = make(chan struct{})
	done := p.refreshDone
	p.mu.Unlock()

	keys, refreshErr := p.fetch(ctx)

	p.mu.Lock()
	if refreshErr == nil {
		p.keys = keys
		p.missing = make(map[string]time.Time)
		p.lastRefreshErr = nil
		p.retryAfter = time.Time{}
	} else {
		p.lastRefreshErr = ErrProviderUnavailable
		p.retryAfter = p.now().Add(p.failureRetry)
	}
	p.generation++
	p.refreshing = false
	close(done)
	if refreshErr != nil {
		p.mu.Unlock()
		return nil, ErrProviderUnavailable
	}
	if key, ok := p.keys[kid]; ok {
		p.mu.Unlock()
		return clonePublicKey(key), nil
	}
	p.rememberMissingLocked(kid, p.now())
	p.mu.Unlock()
	return nil, ErrKeyNotFound
}

func (p *JWKSProvider) fetch(parent context.Context) (map[string]rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(parent, p.requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	req.Header.Set("Accept", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		return nil, ErrProviderUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrProviderUnavailable
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, p.maxResponseBytes+1))
	if err != nil || int64(len(body)) > p.maxResponseBytes {
		return nil, ErrProviderUnavailable
	}

	var document jwksDocument
	if err := json.Unmarshal(body, &document); err != nil || len(document.Keys) == 0 || len(document.Keys) > p.maxKeys {
		return nil, ErrProviderUnavailable
	}

	keys := make(map[string]rsa.PublicKey, len(document.Keys))
	for _, item := range document.Keys {
		if _, duplicate := keys[item.KID]; duplicate {
			return nil, ErrProviderUnavailable
		}
		key, err := p.parseKey(item)
		if err != nil {
			return nil, ErrProviderUnavailable
		}
		keys[item.KID] = key
	}
	return keys, nil
}

type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	KTY    string   `json:"kty"`
	KID    string   `json:"kid"`
	Use    string   `json:"use"`
	Alg    string   `json:"alg"`
	N      string   `json:"n"`
	E      string   `json:"e"`
	KeyOps []string `json:"key_ops"`
}

func (p *JWKSProvider) parseKey(item jwk) (rsa.PublicKey, error) {
	if item.KTY != "RSA" || strings.TrimSpace(item.KID) == "" || len(item.KID) > p.maxKeyIDLength ||
		(item.Use != "" && item.Use != "sig") || (item.Alg != "" && item.Alg != "RS256") ||
		!allowsVerification(item.KeyOps) {
		return rsa.PublicKey{}, ErrProviderUnavailable
	}

	modulusBytes, err := decodeBase64URL(item.N)
	if err != nil || len(modulusBytes) == 0 || len(modulusBytes) > maxRSAModulusBytes {
		return rsa.PublicKey{}, ErrProviderUnavailable
	}
	exponentBytes, err := decodeBase64URL(item.E)
	if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 8 {
		return rsa.PublicKey{}, ErrProviderUnavailable
	}
	modulus := new(big.Int).SetBytes(modulusBytes)
	exponentValue := new(big.Int).SetBytes(exponentBytes)
	if modulus.Sign() <= 0 || modulus.BitLen() < minRSAKeyBits || modulus.Bit(0) == 0 || !exponentValue.IsInt64() {
		return rsa.PublicKey{}, ErrProviderUnavailable
	}
	exponent := exponentValue.Int64()
	maxInt := int64(^uint(0) >> 1)
	if exponent < 3 || exponent > maxInt || exponent%2 == 0 {
		return rsa.PublicKey{}, ErrProviderUnavailable
	}

	return rsa.PublicKey{N: modulus, E: int(exponent)}, nil
}

func decodeBase64URL(value string) ([]byte, error) {
	if value == "" {
		return nil, ErrProviderUnavailable
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.URLEncoding.DecodeString(value)
}

func allowsVerification(operations []string) bool {
	if len(operations) == 0 {
		return true
	}
	for _, operation := range operations {
		if operation == "verify" {
			return true
		}
	}
	return false
}

func clonePublicKey(key rsa.PublicKey) *rsa.PublicKey {
	return &rsa.PublicKey{N: new(big.Int).Set(key.N), E: key.E}
}

func (p *JWKSProvider) rememberMissingLocked(kid string, now time.Time) {
	for existing, until := range p.missing {
		if !now.Before(until) {
			delete(p.missing, existing)
		}
	}
	if len(p.missing) >= p.maxKeys {
		var oldestKey string
		var oldest time.Time
		for existing, until := range p.missing {
			if oldestKey == "" || until.Before(oldest) {
				oldestKey, oldest = existing, until
			}
		}
		delete(p.missing, oldestKey)
	}
	p.missing[kid] = now.Add(p.missingKeyTTL)
}
