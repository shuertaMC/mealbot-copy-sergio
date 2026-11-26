package pairing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
	"github.com/johnamadeo/mealbot/internal/mailer"
)

const (
	defaultClaimLimit = 100
	defaultClaimLease = 15 * time.Minute
)

// MemberLoader loads active members for a pairing round.
type MemberLoader interface {
	ListMembers(ctx context.Context, organization string, activeOnly bool) ([]domain.Member, error)
}

// TraitLoader loads the optional metadata key used for cross-match preference.
type TraitLoader interface {
	CrossMatchTrait(ctx context.Context, organization string) (*string, error)
}

// DueRoundClaimer atomically leases due rounds and releases failed claims.
type DueRoundClaimer interface {
	ClaimDueRounds(ctx context.Context, now, staleBefore time.Time, limit int) ([]domain.Round, error)
	ReleaseRoundClaim(ctx context.Context, organization string, roundID int, claimToken string) error
}

// PairingPersister atomically inserts groups, updates histories, and completes
// a round. Implementations must leave all state unchanged on failure.
type PairingPersister interface {
	PersistPairings(ctx context.Context, organization string, roundID int, claimToken string, pairs []domain.Pair) error
}

// MailDelivery delivers one provider-neutral message.
type MailDelivery interface {
	Deliver(ctx context.Context, message mailer.Message) error
}

// Clock supplies scheduler time for deterministic due-round claims.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

// Now returns the function's current time.
func (function ClockFunc) Now() time.Time { return function() }

// SchedulerConfig configures dependencies and claim policy. SMTPLogin is used
// only to compose the sender; provider credentials remain in the mail adapter.
type SchedulerConfig struct {
	Members     MemberLoader
	Traits      TraitLoader
	Rounds      DueRoundClaimer
	Persistence PairingPersister
	Delivery    MailDelivery
	Clock       Clock
	RandomInt   RandomIntGenerator
	SMTPLogin   string
	ClaimLimit  int
	ClaimLease  time.Duration
}

// Scheduler claims, computes, delivers, and atomically persists due rounds.
// It processes claims sequentially so draw and delivery order are explicit.
type Scheduler struct {
	members     MemberLoader
	traits      TraitLoader
	rounds      DueRoundClaimer
	persistence PairingPersister
	delivery    MailDelivery
	clock       Clock
	randomInt   RandomIntGenerator
	smtpLogin   string
	claimLimit  int
	claimLease  time.Duration
}

// NewScheduler validates dependencies and applies claim defaults.
func NewScheduler(config SchedulerConfig) (*Scheduler, error) {
	if config.Members == nil || config.Traits == nil || config.Rounds == nil || config.Persistence == nil || config.Delivery == nil || config.Clock == nil || config.RandomInt == nil {
		return nil, errors.New("pairing scheduler: all dependencies are required")
	}
	if strings.TrimSpace(config.SMTPLogin) == "" {
		return nil, errors.New("pairing scheduler: SMTP login is required")
	}
	if config.ClaimLimit == 0 {
		config.ClaimLimit = defaultClaimLimit
	}
	if config.ClaimLease == 0 {
		config.ClaimLease = defaultClaimLease
	}
	if config.ClaimLimit < 0 || config.ClaimLease < 0 {
		return nil, errors.New("pairing scheduler: claim limit and lease must be positive")
	}
	return &Scheduler{
		members: config.Members, traits: config.Traits, rounds: config.Rounds,
		persistence: config.Persistence, delivery: config.Delivery, clock: config.Clock,
		randomInt: config.RandomInt, smtpLogin: strings.TrimSpace(config.SMTPLogin),
		claimLimit: config.ClaimLimit, claimLease: config.ClaimLease,
	}, nil
}

// Run claims all currently due rounds up to the configured limit. Any round
// failure is returned after the scheduler attempts to release the failing and
// unprocessed claims.
func (s *Scheduler) Run(ctx context.Context) error {
	now := s.clock.Now().UTC()
	if now.IsZero() {
		return errors.New("pairing scheduler: clock returned zero time")
	}
	rounds, err := s.rounds.ClaimDueRounds(ctx, now, now.Add(-s.claimLease), s.claimLimit)
	if err != nil {
		return fmt.Errorf("pairing claim due rounds: %w", err)
	}
	for index, round := range rounds {
		if err := s.ProcessRound(ctx, round); err != nil {
			// A canceled processing context must not prevent best-effort claim
			// releases; repository implementations still control their own timeout.
			resultErr := err
			releaseCtx := context.WithoutCancel(ctx)
			for _, claimed := range rounds[index:] {
				if releaseErr := s.rounds.ReleaseRoundClaim(releaseCtx, claimed.Organization, claimed.ID, claimed.ClaimToken); releaseErr != nil {
					resultErr = errors.Join(
						resultErr,
						fmt.Errorf("pairing release round claim %s/%d: %w", claimed.Organization, claimed.ID, releaseErr),
					)
				}
			}
			return resultErr
		}
	}
	return nil
}

// ProcessRound computes and delivers a claimed round, then persists it. Zero
// and singleton populations deliberately produce no mail and persist an empty
// group list so the due round completes without dropping a member.
func (s *Scheduler) ProcessRound(ctx context.Context, scheduled domain.Round) error {
	members, err := s.members.ListMembers(ctx, scheduled.Organization, true)
	if err != nil {
		return fmt.Errorf("pairing load active members: %w", err)
	}
	trait, err := s.traits.CrossMatchTrait(ctx, scheduled.Organization)
	if err != nil {
		return fmt.Errorf("pairing load cross-match trait: %w", err)
	}
	minimal := make(MembersMap, len(members))
	byID := make(map[string]domain.Member, len(members))
	for _, member := range members {
		if _, duplicate := byID[member.Email]; duplicate {
			return domain.NewIntegrityError("pairing load active members", fmt.Sprintf("member %q appears more than once", member.Email), nil)
		}
		traitValue := ""
		if trait != nil {
			traitValue = member.Metadata[*trait]
		}
		minimal[member.Email] = MinimalMember{
			ID: member.Email, Trait: traitValue,
			LastRoundWith: member.LastRoundWith,
		}
		byID[member.Email] = member
	}
	_, computed, err := Run(minimal, scheduled.ID, s.randomInt)
	if err != nil {
		return fmt.Errorf("pairing compute round: %w", err)
	}

	persisted := make([]domain.Pair, 0, len(computed.DrawOrderedPairs))
	for _, group := range computed.DrawOrderedPairs {
		recipients := make([]mailer.Recipient, 0, len(group.Members()))
		for _, id := range group.Members() {
			member, ok := byID[id]
			if !ok {
				return domain.NewIntegrityError("pairing compose message", fmt.Sprintf("member %q is missing", id), nil)
			}
			recipients = append(recipients, mailer.Recipient{Email: member.Email, Name: member.Name})
		}
		message, err := mailer.ComposeGroup(scheduled.Organization, s.smtpLogin, recipients)
		if err != nil {
			return fmt.Errorf("pairing compose message: %w", err)
		}
		if err := s.delivery.Deliver(ctx, message); err != nil {
			return fmt.Errorf("pairing deliver group: %w", err)
		}
		persisted = append(persisted, group)
	}
	if err := s.persistence.PersistPairings(ctx, scheduled.Organization, scheduled.ID, scheduled.ClaimToken, persisted); err != nil {
		return fmt.Errorf("pairing persist round: %w", err)
	}
	return nil
}
