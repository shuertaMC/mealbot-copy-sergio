// Package pairing owns matching and scheduling domain behavior.
package pairing

import (
	"context"
	"time"
)

// Clock supplies time to scheduler behavior.
type Clock interface {
	Now() time.Time
}

// Random supplies bounded random integers to deterministic matching tests.
type Random interface {
	Intn(int) int
}

// ScheduledRound identifies work without exposing member data.
type ScheduledRound struct {
	Organization string
	Number       int
}

// SchedulerStore supplies due rounds to the pairing command.
type SchedulerStore interface {
	DueRounds(context.Context, time.Time) ([]ScheduledRound, error)
}

// Notifier delivers notifications before a completed round is persisted.
type Notifier interface {
	Notify(context.Context, string, []string, []string) error
}
