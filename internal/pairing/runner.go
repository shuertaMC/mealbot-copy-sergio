package pairing

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"

	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/store"
)

// Runner loads members, runs the pairing algorithm, emails the groups and
// stores the outcome.
type Runner struct {
	store  *store.Store
	mailer mail.Sender
	logger *slog.Logger
}

// NewRunner creates a Runner. mailer may be nil if emails are never sent.
func NewRunner(st *store.Store, mailer mail.Sender, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{store: st, mailer: mailer, logger: logger}
}

// RunRound pairs the active members of orgname for round roundNum. When
// sendEmails is false the pairs are stored but nobody is emailed.
func (r *Runner) RunRound(ctx context.Context, orgname string, roundNum int, sendEmails bool, rng *rand.Rand) error {
	members, err := r.loadMinimalMembers(ctx, orgname)
	if err != nil {
		return err
	}

	members, round := Run(members, roundNum, rng)
	r.logger.Info(
		"ran pairing round",
		"organization", orgname,
		"round", round.Number,
		"pairs", len(round.DrawOrderedPairs),
		"recentMatches", round.RecentMatches,
	)
	r.logger.Debug("round details", "round", round.String())

	if sendEmails {
		if err := r.sendEmails(ctx, orgname, round, members); err != nil {
			return err
		}
	}

	pairs := make([]store.PairRecord, 0, len(round.DrawOrderedPairs))
	for _, pair := range round.DrawOrderedPairs {
		pairs = append(pairs, store.PairRecord{ID1: pair.ID1, ID2: pair.ID2, ExtraID: pair.ExtraID})
	}
	history := make(map[string]map[string]int, len(members))
	for id, member := range members {
		history[id] = member.LastRoundWith
	}

	return r.store.SaveRound(ctx, orgname, round.Number, pairs, history)
}

// RunScheduler runs every round that is scheduled in the past and not done yet.
func (r *Runner) RunScheduler(ctx context.Context, sendEmails bool, rng *rand.Rand) error {
	due, err := r.store.DueRounds(ctx)
	if err != nil {
		return err
	}

	for _, ref := range due {
		if err := r.RunRound(ctx, ref.Organization, ref.ID, sendEmails, rng); err != nil {
			return err
		}
	}

	r.logger.Info("pairing scheduler finished", "rounds", len(due))
	return nil
}

func (r *Runner) loadMinimalMembers(ctx context.Context, orgname string) (MembersMap, error) {
	crossMatchTrait, err := r.store.GetCrossMatchTrait(ctx, orgname)
	if err != nil {
		return MembersMap{}, err
	}

	members, err := r.store.GetMembers(ctx, orgname, true)
	if err != nil {
		return MembersMap{}, err
	}

	minimalMembers := MembersMap{}
	for _, member := range members {
		minimalMembers[member.Email] = MinimalMember{
			ID:            member.Email,
			Name:          member.Name,
			Trait:         member.Metadata[crossMatchTrait],
			LastRoundWith: member.LastRoundWith,
		}
	}

	return minimalMembers, nil
}

func (r *Runner) sendEmails(ctx context.Context, orgname string, round Round, members MembersMap) error {
	if r.mailer == nil {
		return errors.New("no mail sender configured")
	}

	for _, pair := range round.DrawOrderedPairs {
		toEmails := []string{pair.ID1, pair.ID2}
		toNames := []string{members[pair.ID1].Name, members[pair.ID2].Name}

		if err := r.mailer.SendGroupEmail(ctx, orgname, toEmails, toNames); err != nil {
			return err
		}
	}

	r.logger.Info("emails queued on Mailgun", "organization", orgname, "emails", len(round.DrawOrderedPairs))
	return nil
}
