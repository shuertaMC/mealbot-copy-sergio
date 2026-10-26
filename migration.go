package main

import (
	"encoding/json"
	"fmt"

	"github.com/johnamadeo/mealbot/internal/server"
)

func migrateToLastRoundWithForPairing() error {
	db, err := server.CreateDBConnection(LocalDBConnection)
	defer db.Close()
	if err != nil {
		return err
	}

	rows, err := db.Query("SELECT name FROM organizations")
	if err != nil {
		return err
	}

	organizations := []string{}
	for rows.Next() {
		var organization string
		err := rows.Scan(&organization)
		if err != nil {
			return err
		}

		organizations = append(organizations, organization)
	}

	for _, org := range organizations {
		membersMap := map[string]Member{}
		members, err := GetMembersFromDB(org, true)
		if err != nil {
			return err
		}

		for _, member := range members {
			member.LastRoundWith = map[string]int{}
			membersMap[member.Email] = member
		}

		pairs, err := getPairsFromDB(org)
		if err != nil {
			return err
		}

		for round, roundPairs := range pairs {
			for _, pairing := range roundPairs {
				member1Email := pairing.Member1.Email
				member2Email := pairing.Member2.Email
				extraMemberEmail := pairing.ExtraMember.Email

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

			_, err = db.Exec(
				"UPDATE members SET last_round_with = $1 WHERE organization = $2 AND email = $3",
				server.JSONB(bytes),
				org,
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
