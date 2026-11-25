package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store"
)

// runTestSequence retains the legacy CSV fixture workflow as test-only support.
// Every runtime dependency is supplied by the caller; the helper never reads
// process configuration, opens a database, or constructs a network client.
func runTestSequence(ctx context.Context, repository applicationStore, random pairing.Random, notifier pairing.Notifier, testMode bool) error {
	const organization = "ysc"
	if err := repository.CreateOrganization(ctx, organization, "johnamadeo.daniswara@yale.edu"); err != nil {
		return fmt.Errorf("create fixture organization: %w", err)
	}

	fixture, err := os.Open("../../csv/test_john3.csv")
	if err != nil {
		return fmt.Errorf("open member fixture: %w", err)
	}
	defer fixture.Close()
	if _, err := repository.SynchronizeMembersCSV(ctx, organization, fixture); err != nil {
		return fmt.Errorf("synchronize fixture members: %w", err)
	}

	service := pairing.Service{
		Members:           repository,
		Store:             repository,
		Notifier:          notifier,
		Random:            random,
		SkipNotifications: testMode,
	}
	for sequence := 0; sequence < 2; sequence++ {
		round, err := repository.AddRound(ctx, organization, fmt.Sprintf("2019-01-02 %d:55:00", sequence))
		if err != nil {
			return fmt.Errorf("schedule fixture round %d: %w", sequence, err)
		}
		if err := service.ProcessRound(ctx, pairing.ScheduledRound{Organization: organization, Number: round}); err != nil {
			return fmt.Errorf("process fixture round %d: %w", sequence, err)
		}
	}
	return nil
}

func TestRunTestSequenceUsesInjectedDependencies(t *testing.T) {
	events := make([]string, 0)
	nextRound := 0
	repository := &fakeApplicationStore{
		createOrganizationFunc: func(_ context.Context, organization, admin string) error {
			events = append(events, "create:"+organization+":"+admin)
			return nil
		},
		synchronizeMembersFunc: func(_ context.Context, organization string, source io.Reader) (store.MemberSyncPlan, error) {
			contents, err := io.ReadAll(source)
			if err != nil {
				return store.MemberSyncPlan{}, err
			}
			if len(contents) == 0 {
				t.Fatal("fixture CSV is empty")
			}
			events = append(events, "sync:"+organization)
			return store.MemberSyncPlan{Organization: organization}, nil
		},
		addRoundFunc: func(_ context.Context, organization, scheduled string) (int, error) {
			round := nextRound
			nextRound++
			events = append(events, fmt.Sprintf("schedule:%s:%d:%s", organization, round, scheduled))
			return round, nil
		},
		loadMembersFunc: func(_ context.Context, organization string) (pairing.MembersMap, error) {
			events = append(events, "load:"+organization)
			return pairing.MembersMap{
				"a@example.com": {ID: "a@example.com", Name: "A", LastRoundWith: map[string]int{"b@example.com": -1}},
				"b@example.com": {ID: "b@example.com", Name: "B", LastRoundWith: map[string]int{"a@example.com": -1}},
			}, nil
		},
		saveRoundFunc: func(_ context.Context, organization string, round pairing.Round, _ pairing.MembersMap) error {
			events = append(events, fmt.Sprintf("save:%s:%d", organization, round.Number))
			return nil
		},
	}
	notifier := notifierFunc(func(_ context.Context, organization string, _, _ []string) error {
		events = append(events, "notify:"+organization)
		return nil
	})
	if err := runTestSequence(context.Background(), repository, pairing.RandomFunc(func(int) int { return 0 }), notifier, false); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"create:ysc:johnamadeo.daniswara@yale.edu",
		"sync:ysc",
		"schedule:ysc:0:2019-01-02 0:55:00", "load:ysc", "notify:ysc", "save:ysc:0",
		"schedule:ysc:1:2019-01-02 1:55:00", "load:ysc", "notify:ysc", "save:ysc:1",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}
