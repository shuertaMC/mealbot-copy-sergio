package pairing

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
	"github.com/johnamadeo/mealbot/internal/mailer"
)

func TestSchedulerClaimsDeliversThenPersists(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.FixedZone("offset", 3600))
	repository := &schedulerRepository{
		rounds:  []domain.Round{{Organization: "acme", ID: 4, ScheduledAt: now.Add(-time.Hour)}},
		members: domainMembers(t, 4),
		trait:   stringPointer("team"),
	}
	delivery := &recordingDelivery{}
	scheduler := mustScheduler(t, repository, delivery, ClockFunc(func() time.Time { return now }))
	if err := scheduler.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !repository.claimNow.Equal(now.UTC()) || !repository.staleBefore.Equal(now.UTC().Add(-defaultClaimLease)) || repository.claimLimit != defaultClaimLimit {
		t.Fatalf("claim = %v %v %d", repository.claimNow, repository.staleBefore, repository.claimLimit)
	}
	if len(delivery.messages) != 2 || repository.persistCalls != 1 || len(repository.persisted) != 2 || len(repository.released) != 0 {
		t.Fatalf("messages=%d persists=%d pairs=%d released=%v", len(delivery.messages), repository.persistCalls, len(repository.persisted), repository.released)
	}
	if delivery.messages[0].From != "acme Mealbot <bot@example.test>" || delivery.messages[0].Subject != mailer.EmailSubject {
		t.Fatalf("first message = %#v", delivery.messages[0])
	}
	if !strings.Contains(delivery.messages[0].Text, "Member 0") || !strings.Contains(delivery.messages[0].Text, "Member 1") {
		t.Fatalf("first message body = %q", delivery.messages[0].Text)
	}
	if repository.events[len(repository.events)-1] != "persist" {
		t.Fatalf("event order = %v", repository.events)
	}
}

func TestSchedulerZeroSingletonAndTriplePolicies(t *testing.T) {
	for _, count := range []int{0, 1, 3} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			repository := &schedulerRepository{
				rounds:  []domain.Round{{Organization: "org", ID: 2, ScheduledAt: time.Now()}},
				members: domainMembers(t, count),
			}
			delivery := &recordingDelivery{}
			scheduler := mustScheduler(t, repository, delivery, ClockFunc(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }))
			if err := scheduler.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantMessages := 0
			wantPairs := 0
			if count == 3 {
				wantMessages, wantPairs = 1, 1
			}
			if len(delivery.messages) != wantMessages || len(repository.persisted) != wantPairs || repository.persistCalls != 1 {
				t.Fatalf("messages=%d pairs=%d persists=%d", len(delivery.messages), len(repository.persisted), repository.persistCalls)
			}
			if count == 3 && (len(delivery.messages[0].To) != 3 || repository.persisted[0].ExtraID == "") {
				t.Fatalf("triple message/pair = %#v / %#v", delivery.messages[0], repository.persisted[0])
			}
		})
	}
}

func TestSchedulerFailureReleasesWithoutPersistence(t *testing.T) {
	deliveryFailure := errors.New("provider unavailable")
	persistenceFailure := errors.New("commit failed")
	tests := []struct {
		name             string
		configure        func(*schedulerRepository, *recordingDelivery)
		wantDelivery     int
		wantPersistCalls int
		wantError        error
	}{
		{
			name: "second delivery",
			configure: func(_ *schedulerRepository, delivery *recordingDelivery) {
				delivery.failAt = 2
				delivery.err = deliveryFailure
			},
			wantDelivery: 2, wantPersistCalls: 0, wantError: deliveryFailure,
		},
		{
			name: "persistence",
			configure: func(repository *schedulerRepository, _ *recordingDelivery) {
				repository.persistErr = persistenceFailure
			},
			wantDelivery: 2, wantPersistCalls: 1, wantError: persistenceFailure,
		},
		{
			name: "member load",
			configure: func(repository *schedulerRepository, _ *recordingDelivery) {
				repository.memberErr = errors.New("load failed")
			},
			wantDelivery: 0, wantPersistCalls: 0,
		},
		{
			name: "trait load",
			configure: func(repository *schedulerRepository, _ *recordingDelivery) {
				repository.traitErr = errors.New("trait failed")
			},
			wantDelivery: 0, wantPersistCalls: 0,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &schedulerRepository{
				rounds:  []domain.Round{{Organization: "org", ID: 3, ScheduledAt: time.Now()}},
				members: domainMembers(t, 4),
			}
			delivery := &recordingDelivery{}
			test.configure(repository, delivery)
			scheduler := mustScheduler(t, repository, delivery, ClockFunc(func() time.Time { return time.Now() }))
			err := scheduler.Run(context.Background())
			if err == nil || (test.wantError != nil && !errors.Is(err, test.wantError)) {
				t.Fatalf("Run error = %v, want %v", err, test.wantError)
			}
			if delivery.calls != test.wantDelivery || repository.persistCalls != test.wantPersistCalls || !reflect.DeepEqual(repository.released, []string{"org/3"}) {
				t.Fatalf("delivery=%d persists=%d released=%v", delivery.calls, repository.persistCalls, repository.released)
			}
		})
	}
}

func TestSchedulerFailureReleasesUnprocessedClaims(t *testing.T) {
	processingErr := errors.New("delivery failed")
	releaseErr := errors.New("release failed")
	tests := []struct {
		name        string
		releaseErrs map[string]error
		wantRelease error
	}{
		{name: "cleanup succeeds"},
		{
			name:        "cleanup continues after release error",
			releaseErrs: map[string]error{"second/2": releaseErr},
			wantRelease: releaseErr,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &schedulerRepository{
				rounds: []domain.Round{
					{Organization: "first", ID: 1},
					{Organization: "second", ID: 2},
					{Organization: "third", ID: 3},
				},
				members:     domainMembers(t, 2),
				releaseErrs: test.releaseErrs,
			}
			delivery := &recordingDelivery{failAt: 2, err: processingErr}
			scheduler := mustScheduler(t, repository, delivery, ClockFunc(func() time.Time {
				return time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
			}))

			err := scheduler.Run(context.Background())
			if !errors.Is(err, processingErr) {
				t.Fatalf("Run error = %v, want processing error", err)
			}
			if test.wantRelease != nil && !errors.Is(err, test.wantRelease) {
				t.Fatalf("Run error = %v, want release error", err)
			}
			if delivery.calls != 2 || repository.persistCalls != 1 {
				t.Fatalf("delivery calls=%d persists=%d", delivery.calls, repository.persistCalls)
			}
			if want := []string{"second/2", "third/3"}; !reflect.DeepEqual(repository.released, want) {
				t.Fatalf("released = %v, want %v", repository.released, want)
			}
		})
	}
}

func TestSchedulerClaimAndReleaseErrors(t *testing.T) {
	claimErr := errors.New("claim failed")
	repository := &schedulerRepository{claimErr: claimErr}
	scheduler := mustScheduler(t, repository, &recordingDelivery{}, ClockFunc(func() time.Time { return time.Now() }))
	if err := scheduler.Run(context.Background()); !errors.Is(err, claimErr) {
		t.Fatalf("claim error = %v", err)
	}

	repository = &schedulerRepository{
		rounds:     []domain.Round{{Organization: "org", ID: 1, ScheduledAt: time.Now()}},
		members:    domainMembers(t, 2),
		releaseErr: errors.New("release failed"),
	}
	delivery := &recordingDelivery{failAt: 1, err: errors.New("delivery failed")}
	scheduler = mustScheduler(t, repository, delivery, ClockFunc(func() time.Time { return time.Now() }))
	err := scheduler.Run(context.Background())
	if !errors.Is(err, delivery.err) || !errors.Is(err, repository.releaseErr) {
		t.Fatalf("joined error = %v", err)
	}
}

func TestSchedulerValidationAndClock(t *testing.T) {
	if _, err := NewScheduler(SchedulerConfig{}); err == nil {
		t.Fatal("NewScheduler accepted missing dependencies")
	}
	repository := &schedulerRepository{}
	base := SchedulerConfig{
		Members: repository, Traits: repository, Rounds: repository, Persistence: repository,
		Delivery: &recordingDelivery{}, Clock: ClockFunc(func() time.Time { return time.Now() }),
		RandomInt: func(int) int { return 0 }, SMTPLogin: "bot@example.test",
	}
	withoutSMTP := base
	withoutSMTP.SMTPLogin = " "
	if _, err := NewScheduler(withoutSMTP); err == nil {
		t.Fatal("NewScheduler accepted empty SMTP login")
	}
	badClaim := base
	badClaim.ClaimLimit = -1
	if _, err := NewScheduler(badClaim); err == nil {
		t.Fatal("NewScheduler accepted negative claim limit")
	}
	zeroClock := base
	zeroClock.Clock = ClockFunc(func() time.Time { return time.Time{} })
	scheduler, err := NewScheduler(zeroClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Run(context.Background()); err == nil {
		t.Fatal("Run accepted zero clock")
	}
}

type schedulerRepository struct {
	rounds       []domain.Round
	members      []domain.Member
	trait        *string
	claimErr     error
	memberErr    error
	traitErr     error
	persistErr   error
	releaseErr   error
	releaseErrs  map[string]error
	claimNow     time.Time
	staleBefore  time.Time
	claimLimit   int
	persistCalls int
	persisted    []domain.Pair
	released     []string
	events       []string
}

func (repository *schedulerRepository) ClaimDueRounds(_ context.Context, now, staleBefore time.Time, limit int) ([]domain.Round, error) {
	repository.claimNow, repository.staleBefore, repository.claimLimit = now, staleBefore, limit
	return append([]domain.Round(nil), repository.rounds...), repository.claimErr
}

func (repository *schedulerRepository) ReleaseRoundClaim(_ context.Context, organization string, roundID int) error {
	claim := fmt.Sprintf("%s/%d", organization, roundID)
	repository.released = append(repository.released, claim)
	if err := repository.releaseErrs[claim]; err != nil {
		return err
	}
	return repository.releaseErr
}

func (repository *schedulerRepository) ListMembers(_ context.Context, _ string, activeOnly bool) ([]domain.Member, error) {
	if !activeOnly {
		panic("scheduler requested inactive members")
	}
	return append([]domain.Member(nil), repository.members...), repository.memberErr
}

func (repository *schedulerRepository) CrossMatchTrait(context.Context, string) (*string, error) {
	return repository.trait, repository.traitErr
}

func (repository *schedulerRepository) PersistPairings(_ context.Context, _ string, _ int, pairs []domain.Pair) error {
	repository.persistCalls++
	repository.events = append(repository.events, "persist")
	repository.persisted = append([]domain.Pair(nil), pairs...)
	return repository.persistErr
}

type recordingDelivery struct {
	messages []mailer.Message
	calls    int
	failAt   int
	err      error
}

func (delivery *recordingDelivery) Deliver(_ context.Context, message mailer.Message) error {
	delivery.calls++
	if delivery.failAt == delivery.calls {
		return delivery.err
	}
	delivery.messages = append(delivery.messages, message.Clone())
	return nil
}

func mustScheduler(t *testing.T, repository *schedulerRepository, delivery *recordingDelivery, clock Clock) *Scheduler {
	t.Helper()
	scheduler, err := NewScheduler(SchedulerConfig{
		Members: repository, Traits: repository, Rounds: repository, Persistence: repository,
		Delivery: delivery, Clock: clock, RandomInt: func(int) int { return 0 },
		SMTPLogin: "bot@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return scheduler
}

func domainMembers(t *testing.T, count int) []domain.Member {
	t.Helper()
	members := make([]domain.Member, 0, count)
	for index := 0; index < count; index++ {
		email := string(rune('a'+index)) + "@example.test"
		member, err := domain.NewMember("org", email, "Member "+string(rune('0'+index)), map[string]string{"team": "red"}, map[string]int{})
		if err != nil {
			t.Fatal(err)
		}
		member.Organization = "acme"
		members = append(members, member)
	}
	return members
}

func stringPointer(value string) *string { return &value }
