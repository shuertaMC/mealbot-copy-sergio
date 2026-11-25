package main

import (
	"fmt"

	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/members"
	"github.com/johnamadeo/mealbot/internal/org"
	"github.com/johnamadeo/mealbot/internal/rounds"
)

// runTestSequence is a manual development helper (kept from the original
// server.go; it is intentionally not called by default). It creates an
// organization, imports a CSV and runs two pairing rounds.
func runTestSequence(runner *rounds.Runner, q db.Querier, testMode bool) {
	err := org.CreateOrganization(q, "ysc", "johnamadeo.daniswara@yale.edu")
	if err != nil {
		fmt.Println(err)
	}

	_, err = members.CreateMembersFromCSV(q, "ysc", "./csv/test_john3.csv")
	if err != nil {
		fmt.Println(err)
		return
	}

	i := 0
	for i < 2 {
		err = rounds.AddRound(q, "ysc", fmt.Sprintf("2019-01-02 %d:55:00", i))
		if err != nil {
			fmt.Println(err)
			return
		}

		// NOTE: Do you want to actually send out emails?
		err = runner.RunPairingRound("ysc", i, testMode)
		if err != nil {
			fmt.Println(err)
			return
		}

		i++
	}
}
