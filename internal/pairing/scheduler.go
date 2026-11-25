package pairing

import (
	"context"
	"fmt"
)

// Scheduler selects UTC-past unfinished rounds through its store and processes
// them in returned order, stopping at the first failure.
type Scheduler struct {
	Store     SchedulerStore
	Clock     Clock
	Processor RoundProcessor
	Observer  Observer
}

// Run performs one scheduler pass.
func (s Scheduler) Run(ctx context.Context) error {
	if s.Store == nil {
		return fmt.Errorf("pair scheduler: store is nil")
	}
	if s.Clock == nil {
		return fmt.Errorf("pair scheduler: clock is nil")
	}
	if s.Processor == nil {
		return fmt.Errorf("pair scheduler: processor is nil")
	}
	rounds, err := s.Store.DueRounds(ctx, s.Clock.Now().UTC())
	if err != nil {
		return fmt.Errorf("pair scheduler: due rounds: %w", err)
	}
	for _, round := range rounds {
		if s.Observer != nil {
			s.Observer.ObserveRound(ctx, round, RoundStarted)
		}
		if err := s.Processor.ProcessRound(ctx, round); err != nil {
			if s.Observer != nil {
				s.Observer.ObserveRound(ctx, round, RoundFailed)
			}
			return fmt.Errorf("pair scheduler organization %q round %d: %w", round.Organization, round.Number, err)
		}
		if s.Observer != nil {
			s.Observer.ObserveRound(ctx, round, RoundCompleted)
		}
	}
	return nil
}
