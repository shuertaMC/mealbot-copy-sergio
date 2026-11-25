package pairing

import (
	"context"
	"fmt"
)

// Service runs pairing and notification before one atomic persistence call.
type Service struct {
	Members           MemberLoader
	Store             RoundStore
	Notifier          Notifier
	RoundDelivery     RoundDelivery
	Matcher           Matcher
	Random            Random
	SkipNotifications bool
}

// ProcessRound loads, matches, delivers every required notification, then
// saves exactly once. Delivery failure prevents persistence.
func (s Service) ProcessRound(ctx context.Context, scheduled ScheduledRound) error {
	if s.Members == nil {
		return fmt.Errorf("pair round organization %q round %d: member loader is nil", scheduled.Organization, scheduled.Number)
	}
	if s.Store == nil {
		return fmt.Errorf("pair round organization %q round %d: round store is nil", scheduled.Organization, scheduled.Number)
	}
	members, err := s.Members.LoadMembers(ctx, scheduled.Organization)
	if err != nil {
		return fmt.Errorf("pair round organization %q round %d: load members: %w", scheduled.Organization, scheduled.Number, err)
	}
	var updated MembersMap
	var round Round
	if s.Matcher == nil {
		updated, round = Match(members, scheduled.Number, s.Random)
	} else {
		updated, round = s.Matcher.Match(members, scheduled.Number, s.Random)
	}
	if !s.SkipNotifications {
		if s.RoundDelivery != nil {
			if err := s.RoundDelivery.DeliverRound(ctx, scheduled.Organization, round, updated); err != nil {
				return fmt.Errorf("pair round organization %q round %d: notify round: %w", scheduled.Organization, scheduled.Number, err)
			}
		} else if s.Notifier == nil && len(round.DrawOrderedPairs) > 0 {
			return fmt.Errorf("pair round organization %q round %d: notifier is nil", scheduled.Organization, scheduled.Number)
		} else {
			for _, pair := range round.DrawOrderedPairs {
				emails := []string{pair.ID1, pair.ID2}
				names := []string{updated[pair.ID1].Name, updated[pair.ID2].Name}
				if err := s.Notifier.Notify(ctx, scheduled.Organization, emails, names); err != nil {
					return fmt.Errorf("pair round organization %q round %d: notify pair: %w", scheduled.Organization, scheduled.Number, err)
				}
			}
		}
	}
	if err := s.Store.SaveRound(ctx, scheduled.Organization, round, updated); err != nil {
		return fmt.Errorf("pair round organization %q round %d: save: %w", scheduled.Organization, scheduled.Number, err)
	}
	return nil
}
