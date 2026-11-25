package main

import (
	"errors"
	"reflect"
	"sort"
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
		members[email] = MinimalMember{ID: email, Name: "Person " + strings.ToUpper(letter), Trait: "1995", LastRoundWith: map[string]int{}}
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
	members, err := getMockMembersMap(3)
	if err != nil {
		t.Fatal(err)
	}
	for roundNumber := 0; roundNumber < 3; roundNumber++ {
		var round Round
		members, round = runPairingAlgorithm(members, roundNumber, func(int) int { return 0 })
		if round.Number != roundNumber {
			t.Fatalf("round number = %d, want %d", round.Number, roundNumber)
		}
		if len(round.Pairs) != 1 || len(round.Paired) != 2 {
			t.Fatalf("odd population produced %d pairs and %d paired members; want 1 pair and 2 members", len(round.Pairs), len(round.Paired))
		}
		for pair := range round.Pairs {
			if pair.ID1 >= pair.ID2 {
				t.Fatalf("pair IDs are not canonical: %#v", pair)
			}
			if pair.ExtraID != "" {
				t.Fatalf("legacy odd-member pair ExtraID = %q, want empty", pair.ExtraID)
			}
			if members[pair.ID1].LastRoundWith[pair.ID2] != roundNumber || members[pair.ID2].LastRoundWith[pair.ID1] != roundNumber {
				t.Fatalf("history is not symmetric for %#v", pair)
			}
		}
	}
}

func TestPairingAlgorithmEndToEndTest(t *testing.T) {
	members := MembersMap{
		"a@example.com": minimalMember("a@example.com", "red", []string{"b@example.com", "c@example.com", "d@example.com"}),
		"b@example.com": minimalMember("b@example.com", "red", []string{"a@example.com", "c@example.com", "d@example.com"}),
		"c@example.com": minimalMember("c@example.com", "blue", []string{"a@example.com", "b@example.com", "d@example.com"}),
		"d@example.com": minimalMember("d@example.com", "blue", []string{"a@example.com", "b@example.com", "c@example.com"}),
	}
	updated, round := runPairingAlgorithm(members, 7, func(int) int { return 0 })
	if got, want := sortedPairIDs(round), []string{"a@example.com|b@example.com", "c@example.com|d@example.com"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("equality-based trait selection pairs = %v, want %v", got, want)
	}
	if len(round.Paired) != len(members) {
		t.Fatalf("paired members = %d, want %d", len(round.Paired), len(members))
	}
	for pair := range round.Pairs {
		if updated[pair.ID1].LastRoundWith[pair.ID2] != 7 || updated[pair.ID2].LastRoundWith[pair.ID1] != 7 {
			t.Fatalf("pair history is not symmetric for %#v", pair)
		}
	}
}

func minimalMember(id, trait string, partners []string) MinimalMember {
	history := map[string]int{}
	for _, partner := range partners {
		history[partner] = -1
	}
	return MinimalMember{ID: id, Name: id, Trait: trait, LastRoundWith: history}
}

func sortedPairIDs(round Round) []string {
	pairs := []string{}
	for pair := range round.Pairs {
		pairs = append(pairs, pair.ID1+"|"+pair.ID2)
	}
	sort.Strings(pairs)
	return pairs
}

func TestPairingPrefersNonRecentCandidateOverRecentEqualTrait(t *testing.T) {
	members := MembersMap{
		"a@example.com": minimalMember("a@example.com", "red", []string{"b@example.com", "c@example.com", "d@example.com"}),
		"b@example.com": minimalMember("b@example.com", "red", []string{"a@example.com", "c@example.com", "d@example.com"}),
		"c@example.com": minimalMember("c@example.com", "blue", []string{"a@example.com", "b@example.com", "d@example.com"}),
		"d@example.com": minimalMember("d@example.com", "blue", []string{"a@example.com", "b@example.com", "c@example.com"}),
	}
	for _, pair := range [][2]string{{"a@example.com", "b@example.com"}, {"c@example.com", "d@example.com"}} {
		members.UpdateLastRoundWith(pair[0], pair[1], 9)
	}
	_, round := runPairingAlgorithm(members, 10, func(int) int { return 0 })
	for pair := range round.Pairs {
		if members[pair.ID1].Trait == members[pair.ID2].Trait {
			t.Fatalf("recent equal-trait pair %#v was selected over an unseen different-trait candidate", pair)
		}
	}
}

func TestPairingRetriesRecentMatchesToMaxTries(t *testing.T) {
	members, err := getMockMembersMap(2)
	if err != nil {
		t.Fatal(err)
	}
	members.UpdateLastRoundWith("a@gmail.com", "b@gmail.com", 9)
	calls := 0
	updated, round := runPairingAlgorithm(members, 10, func(int) int { calls++; return 0 })
	if calls != MaxTries {
		t.Fatalf("random generator calls = %d, want retry exhaustion at %d", calls, MaxTries)
	}
	if got := sortedPairIDs(round); !reflect.DeepEqual(got, []string{"a@gmail.com|b@gmail.com"}) {
		t.Fatalf("pairs = %v", got)
	}
	if updated["a@gmail.com"].LastRoundWith["b@gmail.com"] != 10 || updated["b@gmail.com"].LastRoundWith["a@gmail.com"] != 10 {
		t.Fatal("retry-exhausted pairing did not update symmetric history")
	}
}

func TestPairingHandlesEmptyAndSingleMemberPopulations(t *testing.T) {
	for name, members := range map[string]MembersMap{
		"empty": {}, "single": {"only@example.com": minimalMember("only@example.com", "red", nil)},
	} {
		t.Run(name, func(t *testing.T) {
			updated, round := runPairingAlgorithm(members, 0, func(int) int { return 0 })
			if len(updated) != len(members) || len(round.Pairs) != 0 || len(round.Paired) != 0 {
				t.Fatalf("updated=%v round=%v", updated, round)
			}
		})
	}
}

func preservePairingRoundSeams() func() {
	originalGetMembers := getMinimalMembersForRound
	originalAlgorithm := runPairingAlgorithmForRound
	originalSend := sendEmailsForRound
	originalSave := saveRoundForRound
	return func() {
		getMinimalMembersForRound = originalGetMembers
		runPairingAlgorithmForRound = originalAlgorithm
		sendEmailsForRound = originalSend
		saveRoundForRound = originalSave
	}
}

func TestRunPairingRoundSendsMailBeforeSave(t *testing.T) {
	defer preservePairingRoundSeams()()
	events := []string{}
	members, err := getMockMembersMap(2)
	if err != nil {
		t.Fatal(err)
	}
	round := NewRound(3)
	round.AddPair("a@gmail.com", "b@gmail.com")
	getMinimalMembersForRound = func(orgname string) (MembersMap, error) { events = append(events, "load:"+orgname); return members, nil }
	runPairingAlgorithmForRound = func(got MembersMap, roundNumber int, random RandomIntGenerator) (MembersMap, Round) {
		events = append(events, "pair")
		if roundNumber != 3 || len(got) != 2 || random == nil {
			t.Fatal("unexpected algorithm arguments")
		}
		return got, round
	}
	sendEmailsForRound = func(string, Round, MembersMap) error { events = append(events, "mail"); return nil }
	saveRoundForRound = func(Round, MembersMap, string) error { events = append(events, "save"); return nil }
	if err := runPairingRound("alpha", 3, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"load:alpha", "pair", "mail", "save"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("operation order = %v, want %v", events, want)
	}
}

func TestRunPairingRoundMailFailurePreventsSave(t *testing.T) {
	defer preservePairingRoundSeams()()
	members, err := getMockMembersMap(2)
	if err != nil {
		t.Fatal(err)
	}
	round := NewRound(1)
	round.AddPair("a@gmail.com", "b@gmail.com")
	getMinimalMembersForRound = func(string) (MembersMap, error) { return members, nil }
	runPairingAlgorithmForRound = func(MembersMap, int, RandomIntGenerator) (MembersMap, Round) { return members, round }
	sendEmailsForRound = func(string, Round, MembersMap) error { return errors.New("mail unavailable") }
	saved := false
	saveRoundForRound = func(Round, MembersMap, string) error { saved = true; return nil }
	if err := runPairingRound("alpha", 1, false); err == nil || err.Error() != "mail unavailable" {
		t.Fatalf("error = %v, want mail failure", err)
	}
	if saved {
		t.Fatal("round was saved after mail failure")
	}
}

func TestRunPairingRoundTestModeSkipsMailAndStillSaves(t *testing.T) {
	defer preservePairingRoundSeams()()
	members, err := getMockMembersMap(2)
	if err != nil {
		t.Fatal(err)
	}
	getMinimalMembersForRound = func(string) (MembersMap, error) { return members, nil }
	runPairingAlgorithmForRound = func(MembersMap, int, RandomIntGenerator) (MembersMap, Round) { return members, NewRound(0) }
	mailed := false
	saved := false
	sendEmailsForRound = func(string, Round, MembersMap) error { mailed = true; return nil }
	saveRoundForRound = func(Round, MembersMap, string) error { saved = true; return nil }
	if err := runPairingRound("alpha", 0, true); err != nil {
		t.Fatal(err)
	}
	if mailed || !saved {
		t.Fatalf("test mode mailed=%v saved=%v; want false, true", mailed, saved)
	}
}
