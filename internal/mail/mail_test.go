package mail

import (
	"context"
	"errors"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
)

func TestFakeRecordsMessages(t *testing.T) {
	f := &Fake{}
	var m Mailer = f
	if err := m.Send(context.Background(), Message{To: []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	if len(f.Sent) != 1 {
		t.Fatalf("sent = %d", len(f.Sent))
	}

	f.Err = errors.New("boom")
	if err := f.Send(context.Background(), Message{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestMailgunRequiresCredentials(t *testing.T) {
	if err := NewMailgun(config.Mailgun{}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("expected error for missing domain")
	}
	if err := NewMailgun(config.Mailgun{Domain: "d"}).Send(context.Background(), Message{}); err == nil {
		t.Fatal("expected error for missing api key")
	}
}
