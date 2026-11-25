package main

import (
	"encoding/json"
	"fmt"

	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/members"
	"github.com/johnamadeo/mealbot/internal/org"
	"github.com/johnamadeo/mealbot/internal/pairs"
)

func migrateToLastRoundWithForPairing(q db.Querier) error {
	organizations, err := org.GetAllOrganizations(q)
	if err != nil {
		return err
	}

	for _, orgname := range organizations {
		membersMap := map[string]members.Member{}
		orgMembers, err := members.GetMembersFromDB(q, orgname, true)
		if err != nil {
			return err
		}

		for _, member := range orgMembers {
			member.LastRoundWith = map[string]int{}
			membersMap[member.Email] = member
		}

		roundPairs, err := pairs.GetPairsFromDB(q, orgname)
		if err != nil {
			return err
		}

		for round, pairsOfRound := range roundPairs {
			for _, pair := range pairsOfRound {
				member1Email := pair.Member1.Email
				member2Email := pair.Member2.Email
				extraMemberEmail := pair.ExtraMember.Email

				// Need to check if member in the pairing is still active
				if _, ok := membersMap[member1Email]; ok {
					membersMap[member1Email].LastRoundWith[member2Email] = round
				}
				if _, ok := membersMap[member2Email]; ok {
					membersMap[member2Email].LastRoundWith[member1Email] = round
				}
				if _, ok := membersMap[extraMemberEmail]; ok {
					membersMap[member1Email].LastRoundWith[extraMemberEmail] = round
					membersMap[member2Email].LastRoundWith[extraMemberEmail] = round
					membersMap[extraMemberEmail].LastRoundWith[member1Email] = round
					membersMap[extraMemberEmail].LastRoundWith[member2Email] = round
				}
			}
		}

		for _, member := range membersMap {
			bytes, err := json.Marshal(member.LastRoundWith)
			if err != nil {
				return err
			}

			_, err = q.Exec(
				"UPDATE members SET last_round_with = $1 WHERE organization = $2 AND email = $3",
				db.JSONB(bytes),
				orgname,
				member.Email,
			)
			if err != nil {
				return err
			}

		}
	}

	fmt.Println("Done migrating to 'last round with' data structure in pairing algorithm!")
	return nil
}
