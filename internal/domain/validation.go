package domain

import "strings"

// RequireNonEmpty returns a validation error naming field when value is blank
// after trimming surrounding whitespace.
func RequireNonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return NewValidationError(field, "must not be empty")
	}
	return nil
}

// RequireRoundID returns a validation error for a negative round identifier.
func RequireRoundID(roundID int) error {
	if roundID < 0 {
		return NewValidationError("round.id", "must be non-negative")
	}
	return nil
}
