package pairing

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type schedulerStoreFunc func(context.Context, time.Time) ([]ScheduledRound, error)

func (f schedulerStoreFunc) DueRounds(ctx context.Context, before time.Time) ([]ScheduledRound, error) {
	return f(ctx, before)
}

type processorFunc func(context.Context, ScheduledRound) error

func (f processorFunc) ProcessRound(ctx context.Context, round ScheduledRound) error {
	return f(ctx, round)
}

func TestSchedulerUsesUTCClockAndProcessesInStoreOrder(t *testing.T) {
	zone := time.FixedZone("test", -7*60*60)
	now := time.Date(2026, 2, 3, 4, 5, 6, 0, zone)
	rounds := []ScheduledRound{{Organization: "second", Number: 2}, {Organization: "first", Number: 1}}
	processed := []ScheduledRound{}
	events := []string{}
	scheduler := Scheduler{
		Clock: fixedClock{now: now},
		Store: schedulerStoreFunc(func(_ context.Context, before time.Time) ([]ScheduledRound, error) {
			if before.Location() != time.UTC || !before.Equal(now) {
				t.Fatalf("before = %v (%v), want UTC %v", before, before.Location(), now.UTC())
			}
			return rounds, nil
		}),
		Processor: processorFunc(func(_ context.Context, round ScheduledRound) error {
			processed = append(processed, round)
			return nil
		}),
		Observer: ObserverFunc(func(_ context.Context, round ScheduledRound, event RoundEvent) {
			events = append(events, round.Organization+":"+string(event))
		}),
	}
	if err := scheduler.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(processed, rounds) {
		t.Fatalf("processed = %#v, want %#v", processed, rounds)
	}
	wantEvents := []string{"second:started", "second:completed", "first:started", "first:completed"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
}

func TestSchedulerFailsFastAndObservesOnlySafeIdentifiers(t *testing.T) {
	rounds := []ScheduledRound{{Organization: "alpha", Number: 1}, {Organization: "beta", Number: 2}}
	processErr := errors.New("provider included private recipient")
	processed := []ScheduledRound{}
	events := []string{}
	scheduler := Scheduler{
		Clock: fixedClock{now: time.Now()},
		Store: schedulerStoreFunc(func(context.Context, time.Time) ([]ScheduledRound, error) { return rounds, nil }),
		Processor: processorFunc(func(_ context.Context, round ScheduledRound) error {
			processed = append(processed, round)
			return processErr
		}),
		Observer: ObserverFunc(func(_ context.Context, round ScheduledRound, event RoundEvent) {
			events = append(events, round.Organization+":"+string(event))
		}),
	}
	err := scheduler.Run(context.Background())
	if !errors.Is(err, processErr) || !reflect.DeepEqual(processed, rounds[:1]) {
		t.Fatalf("error=%v processed=%#v", err, processed)
	}
	if want := []string{"alpha:started", "alpha:failed"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestSchedulerStopsWhenDueRoundLookupFails(t *testing.T) {
	storeErr := errors.New("store unavailable")
	processed := false
	scheduler := Scheduler{
		Clock: fixedClock{now: time.Now()},
		Store: schedulerStoreFunc(func(context.Context, time.Time) ([]ScheduledRound, error) { return nil, storeErr }),
		Processor: processorFunc(func(context.Context, ScheduledRound) error { processed = true; return nil }),
	}
	if err := scheduler.Run(context.Background()); !errors.Is(err, storeErr) || processed {
		t.Fatalf("error=%v processed=%v", err, processed)
	}
}
