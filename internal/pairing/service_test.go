package pairing

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type loaderFunc func(context.Context, string) (MembersMap, error)

func (f loaderFunc) LoadMembers(ctx context.Context, organization string) (MembersMap, error) {
	return f(ctx, organization)
}

type storeFunc func(context.Context, string, Round, MembersMap) error

func (f storeFunc) SaveRound(ctx context.Context, organization string, round Round, members MembersMap) error {
	return f(ctx, organization, round, members)
}

type notifierFunc func(context.Context, string, []string, []string) error

func (f notifierFunc) Notify(ctx context.Context, organization string, emails, names []string) error {
	return f(ctx, organization, emails, names)
}

func TestServiceDeliversAllMailBeforeOneAtomicSave(t *testing.T) {
	events := []string{}
	members := completeMembers("a", "b", "c", "d")
	for id, member := range members {
		member.Name = "name-" + id
		members[id] = member
	}
	service := Service{
		Members: loaderFunc(func(_ context.Context, organization string) (MembersMap, error) {
			events = append(events, "load:"+organization)
			return members, nil
		}),
		Notifier: notifierFunc(func(_ context.Context, organization string, emails, names []string) error {
			events = append(events, "mail:"+organization+":"+emails[0]+":"+emails[1]+":"+names[0]+":"+names[1])
			return nil
		}),
		Store: storeFunc(func(_ context.Context, organization string, round Round, updated MembersMap) error {
			events = append(events, "save:"+organization)
			if round.Number != 8 || len(round.Pairs) != 2 || updated["a"].LastRoundWith["b"] != 8 {
				t.Fatalf("saved state = %#v, %#v", round, updated)
			}
			return nil
		}),
		Random: RandomFunc(func(int) int { return 0 }),
	}
	if err := service.ProcessRound(context.Background(), ScheduledRound{Organization: "org", Number: 8}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"load:org",
		"mail:org:a:b:name-a:name-b",
		"mail:org:c:d:name-c:name-d",
		"save:org",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestServiceStopsAtFirstMailErrorWithoutSave(t *testing.T) {
	mailErr := errors.New("mail unavailable")
	mailCalls := 0
	saveCalls := 0
	service := Service{
		Members: loaderFunc(func(context.Context, string) (MembersMap, error) { return completeMembers("a", "b", "c", "d"), nil }),
		Notifier: notifierFunc(func(context.Context, string, []string, []string) error {
			mailCalls++
			return mailErr
		}),
		Store: storeFunc(func(context.Context, string, Round, MembersMap) error { saveCalls++; return nil }),
		Random: RandomFunc(func(int) int { return 0 }),
	}
	err := service.ProcessRound(context.Background(), ScheduledRound{Organization: "org", Number: 1})
	if !errors.Is(err, mailErr) || mailCalls != 1 || saveCalls != 0 {
		t.Fatalf("error=%v mailCalls=%d saveCalls=%d", err, mailCalls, saveCalls)
	}
}

func TestServiceTestModeSkipsMailAndSavesEmptyRound(t *testing.T) {
	mailCalls := 0
	saveCalls := 0
	service := Service{
		Members: loaderFunc(func(context.Context, string) (MembersMap, error) { return MembersMap{}, nil }),
		Notifier: notifierFunc(func(context.Context, string, []string, []string) error { mailCalls++; return nil }),
		Store: storeFunc(func(_ context.Context, _ string, round Round, members MembersMap) error {
			saveCalls++
			if round.Number != 4 || len(round.Pairs) != 0 || len(members) != 0 {
				t.Fatalf("saved empty round = %#v %#v", round, members)
			}
			return nil
		}),
		SkipNotifications: true,
	}
	if err := service.ProcessRound(context.Background(), ScheduledRound{Organization: "org", Number: 4}); err != nil {
		t.Fatal(err)
	}
	if mailCalls != 0 || saveCalls != 1 {
		t.Fatalf("mailCalls=%d saveCalls=%d", mailCalls, saveCalls)
	}
}

func TestServicePropagatesLoadAndSaveFailures(t *testing.T) {
	loadErr := errors.New("load failed")
	service := Service{
		Members: loaderFunc(func(context.Context, string) (MembersMap, error) { return nil, loadErr }),
		Store:   storeFunc(func(context.Context, string, Round, MembersMap) error { t.Fatal("save called"); return nil }),
	}
	if err := service.ProcessRound(context.Background(), ScheduledRound{Organization: "org"}); !errors.Is(err, loadErr) {
		t.Fatalf("load error = %v", err)
	}

	saveErr := errors.New("save failed")
	service = Service{
		Members:           loaderFunc(func(context.Context, string) (MembersMap, error) { return MembersMap{}, nil }),
		Store:             storeFunc(func(context.Context, string, Round, MembersMap) error { return saveErr }),
		SkipNotifications: true,
	}
	if err := service.ProcessRound(context.Background(), ScheduledRound{Organization: "org"}); !errors.Is(err, saveErr) {
		t.Fatalf("save error = %v", err)
	}
}
