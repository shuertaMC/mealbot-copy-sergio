package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrorClassifications(t *testing.T) {
	cause := errors.New("database detail")
	tests := []struct {
		name       string
		err        error
		kind       ErrorKind
		sentinel   error
		operation  string
		message    string
		classifier func(error) bool
	}{
		{name: "validation", err: NewValidationError("member.email", "invalid"), kind: ErrorKindValidation, sentinel: ErrValidation, operation: "member.email", message: "invalid", classifier: IsValidation},
		{name: "conflict", err: NewConflictError("organization.create", "already exists", cause), kind: ErrorKindConflict, sentinel: ErrConflict, operation: "organization.create", message: "already exists", classifier: IsConflict},
		{name: "not found", err: NewNotFoundError("member", "person@example.test"), kind: ErrorKindNotFound, sentinel: ErrNotFound, operation: "member", message: `"person@example.test" not found`, classifier: IsNotFound},
		{name: "integrity", err: NewIntegrityError("pair.resolve", "member missing", cause), kind: ErrorKindIntegrity, sentinel: ErrIntegrity, operation: "pair.resolve", message: "member missing", classifier: IsIntegrity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("outer: %w", test.err)
			if KindOf(wrapped) != test.kind {
				t.Errorf("KindOf() = %q, want %q", KindOf(wrapped), test.kind)
			}
			if !errors.Is(wrapped, test.sentinel) || !test.classifier(wrapped) {
				t.Errorf("classification failed for %v", wrapped)
			}
			var typed *Error
			if !errors.As(wrapped, &typed) {
				t.Fatal("errors.As() did not find *Error")
			}
			if typed.Kind() != test.kind || typed.Operation() != test.operation || typed.Message() != test.message {
				t.Errorf("typed error = kind %q operation %q message %q", typed.Kind(), typed.Operation(), typed.Message())
			}
			if !strings.Contains(typed.Error(), string(test.kind)) || !strings.Contains(typed.Error(), test.message) {
				t.Errorf("Error() = %q", typed.Error())
			}
		})
	}
	if !errors.Is(tests[1].err, cause) || !errors.Is(tests[3].err, cause) {
		t.Error("classified error did not retain its internal cause")
	}
}

func TestErrorClassificationFallbacks(t *testing.T) {
	if KindOf(nil) != ErrorKindUnknown || KindOf(errors.New("plain")) != ErrorKindUnknown {
		t.Error("unclassified errors must have unknown kind")
	}
	if KindOf(fmt.Errorf("wrapped: %w", ErrValidation)) != ErrorKindValidation {
		t.Error("wrapped ErrValidation was not classified")
	}
	if KindOf(ErrConflict) != ErrorKindConflict || KindOf(ErrNotFound) != ErrorKindNotFound || KindOf(ErrIntegrity) != ErrorKindIntegrity {
		t.Error("sentinel error was not classified")
	}
	if IsValidation(nil) || IsConflict(nil) || IsNotFound(nil) || IsIntegrity(nil) {
		t.Error("nil error was classified")
	}
	unknown := NewError(ErrorKindUnknown, "operation", "failed", nil)
	if KindOf(unknown) != ErrorKindUnknown || unknown.Unwrap() != nil || errors.Is(unknown, ErrValidation) {
		t.Errorf("unknown error classification = %v", unknown)
	}
	withoutID := NewNotFoundError("round", "")
	if withoutID.Message() != "not found" {
		t.Errorf("NewNotFoundError() message = %q", withoutID.Message())
	}
}

func TestNilTypedErrorAccessors(t *testing.T) {
	var domainError *Error
	if domainError.Error() != "<nil>" || domainError.Unwrap() != nil || domainError.Kind() != ErrorKindUnknown || domainError.Operation() != "" || domainError.Message() != "" {
		t.Error("nil *Error accessors returned unexpected values")
	}
	if domainError.Is(ErrValidation) {
		t.Error("nil *Error matched a sentinel")
	}
}
