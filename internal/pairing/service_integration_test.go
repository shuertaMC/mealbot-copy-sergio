package pairing

import (
	"context"
	"testing"

	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/store"
	"github.com/johnamadeo/mealbot/internal/testutil/pgtest"
)

func TestRunRoundIntegration(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	st := store.New(pool)

	if err := st.CreateOrganization(ctx, "org", "admin@x.com"); err != nil {
		t.Fatal(err)
	}
	members := []store.Member{}
	for _, e := range []string{"a@x.com", "b@x.com", "c@x.com", "d@x.com"} {
		members = append(members, store.Member{
			Organization: "org", Email: e, Name: "P " + e,
			Metadata: map[string]string{"year": "1995"},
		})
	}
	if err := st.SaveMembers(ctx, "org", members); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRound(ctx, "org", "2019-01-02 10:00:00"); err != nil {
		t.Fatal(err)
	}

	fake := &mail.Fake{}
	svc := NewService(st, fake, "bot@x.com")
	svc.RandInt = func(n int) int { return 0 }

	if err := svc.RunScheduler(ctx, false); err != nil {
		t.Fatal(err)
	}

	if len(fake.Sent) != 2 {
		t.Fatalf("expected 2 emails, got %d", len(fake.Sent))
	}
	if fake.Sent[0].From != "org Mealbot <bot@x.com>" || fake.Sent[0].Subject != EmailSubject {
		t.Fatalf("unexpected email %+v", fake.Sent[0])
	}

	pairs, err := st.GetPairs(ctx, "org")
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 || len(pairs[0]) != 2 {
		t.Fatalf("unexpected pairs %+v", pairs)
	}

	// round is now done, so the scheduler is a no-op
	if err := svc.RunScheduler(ctx, false); err != nil {
		t.Fatal(err)
	}
	if len(fake.Sent) != 2 {
		t.Fatal("round ran twice")
	}

	if err := MigrateToLastRoundWith(ctx, st); err != nil {
		t.Fatal(err)
	}
}

func TestSendEmailRequiresSMTPLogin(t *testing.T) {
	svc := NewService(nil, &mail.Fake{}, "")
	err := svc.sendEmail(context.Background(), "org", []string{"a"}, []string{"A"})
	if err == nil {
		t.Fatal("expected error when SMTP login is unset")
	}
}
