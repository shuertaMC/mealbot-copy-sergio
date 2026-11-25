package mealbot

import "fmt"

// runTestSequence is retained only as test support for the legacy CSV fixtures.
// Production startup is composed exclusively from cmd/mealbot and cannot call it.
func runTestSequence(testMode bool) {
	if err := createOrganization("ysc", "johnamadeo.daniswara@yale.edu"); err != nil {
		fmt.Println(err)
	}

	if _, err := createMembersFromCSV("ysc", "./csv/test_john3.csv"); err != nil {
		fmt.Println(err)
		return
	}

	for round := 0; round < 2; round++ {
		if err := addRound("ysc", fmt.Sprintf("2019-01-02 %d:55:00", round)); err != nil {
			fmt.Println(err)
			return
		}
		if err := runPairingRound("ysc", round, testMode); err != nil {
			fmt.Println(err)
			return
		}
	}
}
