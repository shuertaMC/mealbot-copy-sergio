package mealbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	pairingdomain "github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/server"
	mailgun "github.com/mailgun/mailgun-go"
)

const (
	// MaxTries is the max. number of times to attempt re-matching if a non-optimal matching is initially made
	MaxTries = pairingdomain.MaxTries
	// RecentRoundRange : max. no. of rounds between 2 pairings that is considered recent
	RecentRoundRange = pairingdomain.RecentRoundRange
	// EmailIntro : Text to put in the beginning of the email body
	EmailIntro = "Your Mealbot group this week is:"
	// EmailFooter : Text to put at the end of the email body
	EmailFooter = "Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com"
	// EmailSubject : Subject of the email
	EmailSubject = "Your new Mealbot group"
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
	crossMatchTrait, err := GetCrossMatchTrait(orgname)
	if err != nil {
		return MembersMap{}, err
	}

	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return MembersMap{}, err
	}

	minimalMembers := MembersMap{}
	members, err := GetMembersFromDB(orgname, true)
	if err != nil {
		return MembersMap{}, err
	}

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

func saveRoundInDB(round Round, members MembersMap, orgname string) error {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return err
	}

	for pair := range round.Pairs {
		columns := "(organization, id1, id2, extraId, round)"
		placeholder := "($1, $2, $3, $4, $5)"

		_, err := db.Exec(
			fmt.Sprintf("INSERT INTO pairs %s VALUES %s", columns, placeholder),
			orgname,
			pair.ID1,
			pair.ID2,
			pair.ExtraID,
			round.Number,
		)

		if err != nil {
			return err
		}
	}

	for _, member := range members {
		bytes, err := json.Marshal(member.LastRoundWith)
		if err != nil {
			return err
		}

		_, err = db.Exec(
			"UPDATE members SET last_round_with = $1 WHERE organization = $2 AND email = $3",
			server.JSONB(bytes),
			orgname,
			member.ID,
		)
		if err != nil {
			return err
		}
	}

	_, err = db.Exec(
		"UPDATE rounds SET done = $1 WHERE organization = $2 AND id = $3",
		true,
		orgname,
		round.Number,
	)
	if err != nil {
		return err
	}

	return nil
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

	from := fmt.Sprintf("%s Mealbot <%s>", orgname, smtpAddress)
	text := fmt.Sprintf(
		"%s \n%s\n\n %s",
		EmailIntro,
		strings.Join(toNames, "\r\n"),
		EmailFooter,
	)

	mg := mailgun.NewMailgun(domain, apiKey)
	message := mg.NewMessage(
		from,
		EmailSubject,
		text,
		toEmails...,
	)

	_, _, err := mg.Send(message)

	if err != nil {
		return err
	}

	fmt.Println("Emails queued on Mailgun!")
	return nil
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
	db, err := server.CreateDBConnection(LocalDBConnection)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx,
		"SELECT organization, id FROM rounds WHERE scheduled_date < $1 AND done = false",
		before,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rounds := []pairingdomain.ScheduledRound{}
	for rows.Next() {
		var round pairingdomain.ScheduledRound
		if err := rows.Scan(&round.Organization, &round.Number); err != nil {
			return nil, err
		}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rounds, nil
}
