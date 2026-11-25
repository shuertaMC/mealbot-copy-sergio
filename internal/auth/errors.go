package auth

import "errors"

var (
	// ErrInvalidToken is returned for every credential validation failure. It
	// intentionally hides parsing and key-provider details from callers.
	ErrInvalidToken = errors.New("auth: invalid access token")
	// ErrKeyNotFound indicates that a refreshed JSON Web Key Set did not
	// contain the requested key identifier.
	ErrKeyNotFound = errors.New("auth: key not found")
	// ErrInvalidJWKS indicates that a key-set response cannot safely be used.
	ErrInvalidJWKS = errors.New("auth: invalid JSON Web Key Set")
)
