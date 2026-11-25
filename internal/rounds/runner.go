package rounds

import (
	"encoding/json"
	"fmt"

	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/members"
	"github.com/johnamadeo/mealbot/internal/org"
	"github.com/johnamadeo/mealbot/internal/pairing"
)

// Runner executes pairing rounds: it loads members from the database, runs the
// pure pairing algorithm, emails the pairs and stores the results.
type Runner struct {
	DB     db.Querier
	Mailer Mailer
	// Rand is the random source handed to pairing.RunPairingAlgorithm. A nil
	// value selects pairing.DefaultRandomIntGenerator.
	Rand pairing.RandomIntGenerator
}

// NewRunner returns a Runner using the default random source.
func NewRunner(q db.Querier, mailer Mailer) *Runner {
	return &Runner{DB: q, Mailer: mailer}
}

// RunPairingRound runs round roundNum for orgname. In testMode no emails are sent.
func (rn *Runner) RunPairingRound(orgname string, roundNum int, testMode bool) error {
	minimalMembers, err := GetMinimalMembersFromDB(rn.DB, orgname)
	if err != nil {
		return err
	}

	updatedMembers, round := pairing.RunPairingAlgorithm(minimalMembers, roundNum, rn.Rand)

	if !testMode {
		err = rn.SendEmails(orgname, round, updatedMembers)
		if err != nil {
			return err
		}
	}

	err = SaveRoundInDB(rn.DB, round, updatedMembers, orgname)
	if err != nil {
		return err
	}

	return nil
}

// SendEmails emails every pair of the round.
func (rn *Runner) SendEmails(orgname string, round pairing.Round, members pairing.MembersMap) error {
	for pair := range round.Pairs {
		toEmails := []string{pair.ID1, pair.ID2}
		toNames := []string{members[pair.ID1].Name, members[pair.ID2].Name}

		err := rn.Mailer.SendEmail(orgname, toEmails, toNames)
		if err != nil {
			return err
		}
	}

	return nil
}

// RunPairingScheduler runs every round that is scheduled in the past and not yet done.
func (rn *Runner) RunPairingScheduler(testMode bool) error {
	rows, err := rn.DB.Query(
		"SELECT organization, id FROM rounds WHERE scheduled_date < now() AT TIME ZONE 'utc' AND done = false",
	)
	if err != nil {
		return err
	}

	type pendingRound struct {
		orgname  string
		roundNum int
	}

	// Read all pending rounds first so the result set is released before the
	// per-round queries run.
	pending := []pendingRound{}
	for rows.Next() {
		var p pendingRound
		err := rows.Scan(&p.orgname, &p.roundNum)
		if err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, p)
	}
	rows.Close()

	for _, p := range pending {
		err = rn.RunPairingRound(p.orgname, p.roundNum, testMode)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetMinimalMembersFromDB loads the active members of orgname in the shape the pairing algorithm expects.
func GetMinimalMembersFromDB(q db.Querier, orgname string) (pairing.MembersMap, error) {
	crossMatchTrait, err := org.GetCrossMatchTrait(q, orgname)
	if err != nil {
		return pairing.MembersMap{}, err
	}

	minimalMembers := pairing.MembersMap{}
	activeMembers, err := members.GetMembersFromDB(q, orgname, true)
	if err != nil {
		return pairing.MembersMap{}, err
	}

	for _, member := range activeMembers {
		minimalMembers[member.Email] = pairing.MinimalMember{
			ID:            member.Email,
			Name:          member.Name,
			Trait:         member.Metadata[crossMatchTrait],
			LastRoundWith: member.LastRoundWith,
		}
	}

	return minimalMembers, nil
}

// SaveRoundInDB stores the pairs of the round, updates every member's
// last_round_with and marks the round as done.
func SaveRoundInDB(q db.Querier, round pairing.Round, members pairing.MembersMap, orgname string) error {
	for pair := range round.Pairs {
		columns := "(organization, id1, id2, extraId, round)"
		placeholder := "($1, $2, $3, $4, $5)"

		_, err := q.Exec(
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

		_, err = q.Exec(
			"UPDATE members SET last_round_with = $1 WHERE organization = $2 AND email = $3",
			db.JSONB(bytes),
			orgname,
			member.ID,
		)
		if err != nil {
			return err
		}
	}

	_, err := q.Exec(
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
