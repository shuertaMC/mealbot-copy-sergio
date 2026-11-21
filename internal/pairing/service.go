package pairing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"

	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/store"
)

const (
	// EmailIntro : Text to put in the beginning of the email body
	EmailIntro = "Your Mealbot group this week is:"
	// EmailFooter : Text to put at the end of the email body
	EmailFooter = "Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com"
	// EmailSubject : Subject of the email
	EmailSubject = "Your new Mealbot group"
)

// Service runs pairing rounds against the database and emails the results.
type Service struct {
	Store  *store.Store
	Mailer mail.Mailer
	// SMTPLogin is the sender address (MAILGUN_SMTP_LOGIN).
	SMTPLogin string
	// RandInt is the random source used by the algorithm.
	RandInt RandomIntGenerator
}

// NewService creates a Service using math/rand as the random source.
func NewService(st *store.Store, mailer mail.Mailer, smtpLogin string) *Service {
	return &Service{Store: st, Mailer: mailer, SMTPLogin: smtpLogin, RandInt: rand.Intn}
}

// RunRound runs the pairing algorithm for one round of an organization, emails
// the pairs (unless testMode) and saves the result.
func (s *Service) RunRound(ctx context.Context, orgname string, roundNum int, testMode bool) error {
	members, err := s.getMinimalMembers(ctx, orgname)
	if err != nil {
		return err
	}

	members, round := runPairingAlgorithm(members, roundNum, s.RandInt)

	if !testMode {
		err = s.sendEmails(ctx, orgname, round, members)
		if err != nil {
			return err
		}
	}

	return s.saveRound(ctx, round, members, orgname)
}

// RunScheduler runs every round that is due and not yet done.
func (s *Service) RunScheduler(ctx context.Context, testMode bool) error {
	pending, err := s.Store.GetPendingRounds(ctx)
	if err != nil {
		return err
	}

	for _, p := range pending {
		if err := s.RunRound(ctx, p.Organization, p.ID, testMode); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) sendEmails(ctx context.Context, orgname string, round Round, members MembersMap) error {
	for pair := range round.Pairs {
		toEmails := []string{pair.ID1, pair.ID2}
		toNames := []string{members[pair.ID1].Name, members[pair.ID2].Name}

		if err := s.sendEmail(ctx, orgname, toEmails, toNames); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) sendEmail(ctx context.Context, orgname string, toEmails []string, toNames []string) error {
	if s.SMTPLogin == "" {
		return errors.New("environment variable MAILGUN_SMTP_LOGIN not set")
	}

	from := fmt.Sprintf("%s Mealbot <%s>", orgname, s.SMTPLogin)
	text := fmt.Sprintf(
		"%s \n%s\n\n %s",
		EmailIntro,
		strings.Join(toNames, "\r\n"),
		EmailFooter,
	)

	err := s.Mailer.Send(ctx, mail.Message{
		From:    from,
		Subject: EmailSubject,
		Text:    text,
		To:      toEmails,
	})
	if err != nil {
		return err
	}

	slog.Info("Emails queued on Mailgun!")
	return nil
}

func (s *Service) getMinimalMembers(ctx context.Context, orgname string) (MembersMap, error) {
	crossMatchTrait, err := s.Store.GetCrossMatchTrait(ctx, orgname)
	if err != nil {
		return MembersMap{}, err
	}

	members, err := s.Store.GetMembers(ctx, orgname, true)
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

func (s *Service) saveRound(ctx context.Context, round Round, members MembersMap, orgname string) error {
	for pair := range round.Pairs {
		err := s.Store.InsertPair(ctx, orgname, pair.ID1, pair.ID2, pair.ExtraID, round.Number)
		if err != nil {
			return err
		}
	}

	for _, member := range members {
		if err := s.Store.UpdateLastRoundWith(ctx, orgname, member.ID, member.LastRoundWith); err != nil {
			return err
		}
	}

	return s.Store.MarkRoundDone(ctx, orgname, round.Number)
}
