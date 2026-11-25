package mealbot

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"time"

	"github.com/johnamadeo/mealbot/internal/mailer"
	pairingdomain "github.com/johnamadeo/mealbot/internal/pairing"
)

const (
	// MaxTries is the max. number of times to attempt re-matching if a non-optimal matching is initially made
	MaxTries = pairingdomain.MaxTries
	// RecentRoundRange : max. no. of rounds between 2 pairings that is considered recent
	RecentRoundRange = pairingdomain.RecentRoundRange
	// EmailIntro : Text to put in the beginning of the email body
	EmailIntro = mailer.EmailIntro
	// EmailFooter : Text to put at the end of the email body
	EmailFooter = mailer.EmailFooter
	// EmailSubject : Subject of the email
	EmailSubject = mailer.EmailSubject
)

// Pair is retained as a compatibility alias while internal/pairing owns it.
type Pair = pairingdomain.Pair

// NewPair :
func NewPair(name1 string, name2 string) Pair {
	return pairingdomain.NewPair(name1, name2)
}

// Round : Data structure for storing info on one round of pairings between members
type Round = pairingdomain.Round

// NewRound :
func NewRound(roundNumber int) Round {
	return pairingdomain.NewRound(roundNumber)
}

// MinimalMember : A pared down version of the Member struct (see members.go) for the pairing process
type MinimalMember = pairingdomain.MinimalMember

// MembersMap :
type MembersMap = pairingdomain.MembersMap

type RandomIntGenerator func(int) int

var getMinimalMembersForRound = getMinimalMembersFromDB
var runPairingAlgorithmForRound = runPairingAlgorithm
var sendEmailsForRound = sendEmails
var saveRoundForRound = saveRoundInDB

func runPairingAlgorithm(members MembersMap, roundNum int, genRandomInt RandomIntGenerator) (MembersMap, Round) {
	return pairingdomain.Match(members, roundNum, pairingdomain.RandomFunc(genRandomInt))
}

func runPairingRound(orgname string, roundNum int, testMode bool) error {
	service := pairingdomain.Service{
		Members:           legacyMemberLoader{},
		Store:             legacyRoundStore{},
		RoundDelivery:     legacyRoundDelivery{},
		Matcher:           legacyMatcher{},
		Random:            pairingdomain.RandomFunc(rand.Intn),
		SkipNotifications: testMode,
	}
	err := service.ProcessRound(context.Background(), pairingdomain.ScheduledRound{Organization: orgname, Number: roundNum})
	// Preserve the legacy root helper's exact dependency error while the new
	// domain service keeps useful contextual wrapping for modern callers.
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err
}

type legacyMemberLoader struct{}

func (legacyMemberLoader) LoadMembers(_ context.Context, organization string) (pairingdomain.MembersMap, error) {
	return getMinimalMembersForRound(organization)
}

type legacyMatcher struct{}

func (legacyMatcher) Match(members pairingdomain.MembersMap, round int, random pairingdomain.Random) (pairingdomain.MembersMap, pairingdomain.Round) {
	return runPairingAlgorithmForRound(members, round, random.Intn)
}

type legacyRoundDelivery struct{}

func (legacyRoundDelivery) DeliverRound(_ context.Context, organization string, round pairingdomain.Round, members pairingdomain.MembersMap) error {
	return sendEmailsForRound(organization, round, members)
}

type legacyRoundStore struct{}

func (legacyRoundStore) SaveRound(_ context.Context, organization string, round pairingdomain.Round, members pairingdomain.MembersMap) error {
	return saveRoundForRound(round, members, organization)
}

func sendEmails(orgname string, round Round, members MembersMap) error {
	for pair := range round.Pairs {
		toEmails := []string{pair.ID1, pair.ID2}
		toNames := []string{members[pair.ID1].Name, members[pair.ID2].Name}

		err := sendEmail(orgname, toEmails, toNames)
		if err != nil {
			return err
		}
	}

	return nil
}

func getMinimalMembersFromDB(orgname string) (MembersMap, error) {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return MembersMap{}, err
	}
	defer db.Close()
	return repository.LoadMembers(context.Background(), orgname)
}

func saveRoundInDB(round Round, members MembersMap, orgname string) error {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return err
	}
	defer db.Close()
	return repository.SaveRound(context.Background(), orgname, round, members)
}

func sendEmail(orgname string, toEmails []string, toNames []string) error {
	smtpAddress, ok := os.LookupEnv("MAILGUN_SMTP_LOGIN")
	if !ok {
		return errors.New("environment variable MAILGUN_SMTP_LOGIN not set")
	}
	domain, ok := os.LookupEnv("MAILGUN_DOMAIN")
	if !ok {
		return errors.New("environment variable MAILGUN_DOMAIN not set")
	}
	apiKey, ok := os.LookupEnv("MAILGUN_API_KEY")
	if !ok {
		return errors.New("environment variable MAILGUN_API_KEY not set")
	}

	notifier, err := mailer.New(mailer.Config{
		Domain:    domain,
		APIKey:    apiKey,
		SMTPLogin: smtpAddress,
	})
	if err != nil {
		return err
	}
	return notifier.Notify(context.Background(), orgname, toEmails, toNames)
}

func runPairingScheduler(testMode bool) error {
	scheduler := pairingdomain.Scheduler{
		Store:     legacyDueRoundStore{},
		Clock:     systemClock{},
		Processor: legacyRoundProcessor{testMode: testMode},
	}
	return scheduler.Run(context.Background())
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type legacyRoundProcessor struct{ testMode bool }

func (p legacyRoundProcessor) ProcessRound(_ context.Context, round pairingdomain.ScheduledRound) error {
	return runPairingRound(round.Organization, round.Number, p.testMode)
}

type legacyDueRoundStore struct{}

func (legacyDueRoundStore) DueRounds(ctx context.Context, before time.Time) ([]pairingdomain.ScheduledRound, error) {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return repository.DueRounds(ctx, before)
}
