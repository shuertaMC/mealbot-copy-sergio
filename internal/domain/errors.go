package domain

import (
	"errors"
	"fmt"
)

// ErrorKind classifies a domain failure for repository and transport mapping.
type ErrorKind string

const (
	// ErrorKindUnknown identifies an unclassified failure.
	ErrorKindUnknown ErrorKind = "unknown"
	// ErrorKindValidation identifies invalid caller input.
	ErrorKindValidation ErrorKind = "validation"
	// ErrorKindConflict identifies a valid request that conflicts with state.
	ErrorKindConflict ErrorKind = "conflict"
	// ErrorKindNotFound identifies a requested resource that does not exist.
	ErrorKindNotFound ErrorKind = "not_found"
	// ErrorKindIntegrity identifies inconsistent persisted or computed state.
	ErrorKindIntegrity ErrorKind = "integrity"
)

var (
	// ErrValidation is matched by validation failures.
	ErrValidation = errors.New("domain validation failed")
	// ErrConflict is matched by state-conflict failures.
	ErrConflict = errors.New("domain conflict")
	// ErrNotFound is matched by missing-resource failures.
	ErrNotFound = errors.New("domain resource not found")
	// ErrIntegrity is matched by inconsistent-state failures.
	ErrIntegrity = errors.New("domain integrity failure")
)

// Error is a classified domain error. It retains an internal cause for logs
// while exposing a stable kind and message to callers.
type Error struct {
	kind      ErrorKind
	operation string
	message   string
	cause     error
}

// NewError constructs a classified error. Unknown kinds are permitted for
// internal failures that should not map to a domain-specific status.
func NewError(kind ErrorKind, operation, message string, cause error) *Error {
	return &Error{
		kind:      kind,
		operation: operation,
		message:   message,
		cause:     cause,
	}
}

// NewValidationError constructs an invalid-input error.
func NewValidationError(field, message string) *Error {
	return NewError(ErrorKindValidation, field, message, nil)
}

// NewConflictError constructs a state-conflict error.
func NewConflictError(operation, message string, cause error) *Error {
	return NewError(ErrorKindConflict, operation, message, cause)
}

// NewNotFoundError constructs a missing-resource error.
func NewNotFoundError(resource, identifier string) *Error {
	message := "not found"
	if identifier != "" {
		message = fmt.Sprintf("%q not found", identifier)
	}
	return NewError(ErrorKindNotFound, resource, message, nil)
}

// NewIntegrityError constructs an inconsistent-state error.
func NewIntegrityError(operation, message string, cause error) *Error {
	return NewError(ErrorKindIntegrity, operation, message, cause)
}

// Error returns a stable description without discarding the classified kind.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	prefix := string(e.kind)
	if e.operation != "" {
		prefix += " " + e.operation
	}
	if e.message == "" {
		return prefix
	}
	return prefix + ": " + e.message
}

// Unwrap returns the underlying internal cause, when present.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Is supports errors.Is checks against the stable domain sentinels.
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrValidation:
		return e.kind == ErrorKindValidation
	case ErrConflict:
		return e.kind == ErrorKindConflict
	case ErrNotFound:
		return e.kind == ErrorKindNotFound
	case ErrIntegrity:
		return e.kind == ErrorKindIntegrity
	default:
		return errors.Is(e.cause, target)
	}
}

// Kind returns the stable error classification.
func (e *Error) Kind() ErrorKind {
	if e == nil {
		return ErrorKindUnknown
	}
	return e.kind
}

// Operation returns the field, resource, or operation associated with the
// failure.
func (e *Error) Operation() string {
	if e == nil {
		return ""
	}
	return e.operation
}

// Message returns the stable detail text without an internal cause.
func (e *Error) Message() string {
	if e == nil {
		return ""
	}
	return e.message
}

// KindOf returns a failure's domain classification or ErrorKindUnknown.
func KindOf(err error) ErrorKind {
	var domainError *Error
	if errors.As(err, &domainError) {
		return domainError.Kind()
	}
	switch {
	case errors.Is(err, ErrValidation):
		return ErrorKindValidation
	case errors.Is(err, ErrConflict):
		return ErrorKindConflict
	case errors.Is(err, ErrNotFound):
		return ErrorKindNotFound
	case errors.Is(err, ErrIntegrity):
		return ErrorKindIntegrity
	default:
		return ErrorKindUnknown
	}
}

// IsValidation reports whether err represents invalid input.
func IsValidation(err error) bool { return errors.Is(err, ErrValidation) }

// IsConflict reports whether err represents a state conflict.
func IsConflict(err error) bool { return errors.Is(err, ErrConflict) }

// IsNotFound reports whether err represents a missing resource.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsIntegrity reports whether err represents inconsistent state.
func IsIntegrity(err error) bool { return errors.Is(err, ErrIntegrity) }
