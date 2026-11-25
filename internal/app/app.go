// Package app contains the Mealbot composition root and process lifecycle.
package app

import (
	"context"
	"errors"
)

// ErrNilContext reports an invalid application lifecycle context.
var ErrNilContext = errors.New("mealbot: nil application context")

// Run initializes the modular application lifecycle.
//
// The legacy executable remains the deployed runtime while its behavior moves
// behind the internal package boundaries. Run therefore performs no network,
// database, or provider work during the scaffold phase.
func Run(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}

	return ctx.Err()
}
