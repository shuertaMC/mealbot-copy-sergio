package pairing

import (
	"errors"
	"strings"
	"testing"
)

func getMockMembersMap(numMembers int) (MembersMap, error) {
	alphabet := "abcdefghijklmnopqrstuvwxyz"
	if numMembers > len(alphabet) {
		return MembersMap{}, errors.New("Cannot mock more than 26 members")
	}

	members := MembersMap{}
	for i := 0; i < numMembers; i++ {
		letter := alphabet[i : i+1]
		email := letter + "@gmail.com"
		members[email] = MinimalMember{
			ID:            email,
			Name:          "Person " + strings.ToUpper(letter),
			Trait:         "1995",
			LastRoundWith: map[string]int{},
		}

		for j := 0; j < numMembers; j++ {
			if i == j {
				continue
			}
			otherEmail := alphabet[j:j+1] + "@gmail.com"
			members[email].LastRoundWith[otherEmail] = -1
		}
	}
	return members, nil
}

func TestRunPairingAlgorithmCorrectness(t *testing.T) {
	t.Log("Test correctness of the pairing algorithm's logic")

	randomIntGenerator := func(n int) int {
		return 0
	}

	members, err := getMockMembersMap(3)
	if err != nil {
		t.Error(err)
	}

	RunPairingAlgorithm(members, 0, randomIntGenerator)
	RunPairingAlgorithm(members, 1, randomIntGenerator)
	RunPairingAlgorithm(members, 2, randomIntGenerator)
}

func TestPairingAlgorithmEndToEndTest(t *testing.T) {

}
