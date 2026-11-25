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

// RandomFunc adapts a function to Random.
type RandomFunc func(int) int

// Intn calls f. A nil RandomFunc deterministically selects index zero.
func (f RandomFunc) Intn(n int) int {
	if f == nil {
		return 0
	}
	return f(n)
}

// Matcher supplies matching behavior to the round service. Production callers
// normally leave it nil to use Match; the interface exists for the temporary
// legacy bridge and focused orchestration tests.
type Matcher interface {
	Match(MembersMap, int, Random) (MembersMap, Round)
}

// MatcherFunc adapts a function to Matcher.
type MatcherFunc func(MembersMap, int, Random) (MembersMap, Round)

// Match calls f, or the package matcher when f is nil.
func (f MatcherFunc) Match(members MembersMap, round int, random Random) (MembersMap, Round) {
	if f == nil {
		return Match(members, round, random)
	}
	return f(members, round, random)
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

// MemberLoader supplies the active member view used by matching. The returned
// members already contain the organization's selected cross-match trait.
type MemberLoader interface {
	LoadMembers(context.Context, string) (MembersMap, error)
}

// RoundStore atomically inserts every pair, writes every member history, and
// marks the round complete.
type RoundStore interface {
	SaveRound(context.Context, string, Round, MembersMap) error
}

// Notifier delivers one pair notification before a completed round is saved.
type Notifier interface {
	Notify(context.Context, string, []string, []string) error
}

// RoundDelivery is an aggregate delivery seam retained only for compatibility
// adapters whose legacy contract sends an already-built round at once.
type RoundDelivery interface {
	DeliverRound(context.Context, string, Round, MembersMap) error
}

// RoundProcessor performs one scheduled unit of pairing work.
type RoundProcessor interface {
	ProcessRound(context.Context, ScheduledRound) error
}

// RoundEvent is a privacy-safe scheduler observation.
type RoundEvent string

const (
	RoundStarted   RoundEvent = "started"
	RoundCompleted RoundEvent = "completed"
	RoundFailed    RoundEvent = "failed"
)

// Observer receives only organization and round identifiers, never member or
// provider data. Implementations may log these events.
type Observer interface {
	ObserveRound(context.Context, ScheduledRound, RoundEvent)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(context.Context, ScheduledRound, RoundEvent)

// ObserveRound calls f when it is non-nil.
func (f ObserverFunc) ObserveRound(ctx context.Context, round ScheduledRound, event RoundEvent) {
	if f != nil {
		f(ctx, round, event)
	}
}
