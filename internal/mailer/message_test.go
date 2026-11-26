package mailer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestComposeGroupPreservesEnvelopeAndBody(t *testing.T) {
	group := []Recipient{
		{Email: "a@example.test", Name: "Ada"},
		{Email: "b@example.test", Name: "Bob"},
		{Email: "c@example.test", Name: "Cy"},
	}
	message, err := ComposeGroup("Acme", "bot@example.test", group)
	if err != nil {
		t.Fatalf("ComposeGroup: %v", err)
	}
	if message.From != "Acme Mealbot <bot@example.test>" || message.Subject != EmailSubject {
		t.Fatalf("envelope = %#v", message)
	}
	if !reflect.DeepEqual(message.To, []string{"a@example.test", "b@example.test", "c@example.test"}) {
		t.Fatalf("recipients = %v", message.To)
	}
	wantBody := EmailIntro + " \nAda\r\nBob\r\nCy\n\n " + EmailFooter
	if message.Text != wantBody {
		t.Fatalf("body = %q, want %q", message.Text, wantBody)
	}
}

func TestComposeGroupAndMessageValidation(t *testing.T) {
	valid := []Recipient{{Email: "a@example.test", Name: "A"}, {Email: "b@example.test", Name: "B"}}
	tests := []struct {
		name  string
		org   string
		smtp  string
		group []Recipient
	}{
		{name: "organization empty", smtp: "bot@example.test", group: valid},
		{name: "organization header injection", org: "org\nBCC: x", smtp: "bot@example.test", group: valid},
		{name: "smtp empty", org: "org", group: valid},
		{name: "smtp header injection", org: "org", smtp: "bot\r@example.test", group: valid},
		{name: "too few", org: "org", smtp: "bot@example.test", group: valid[:1]},
		{name: "too many", org: "org", smtp: "bot@example.test", group: append(valid, valid...)},
		{name: "email empty", org: "org", smtp: "bot@example.test", group: []Recipient{{Name: "A"}, valid[1]}},
		{name: "name empty", org: "org", smtp: "bot@example.test", group: []Recipient{{Email: "a@example.test"}, valid[1]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ComposeGroup(test.org, test.smtp, test.group); err == nil {
				t.Fatal("ComposeGroup accepted invalid input")
			}
		})
	}

	messageTests := []Message{
		{To: []string{"a", "b"}, Subject: "s", Text: "t"},
		{From: "from\n", To: []string{"a", "b"}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", "b"}, Text: "t"},
		{From: "from", To: []string{"a"}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", "b", "c", "d"}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", ""}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", "b"}, Subject: "s"},
	}
	for index, message := range messageTests {
		if err := message.Validate(); err == nil {
			t.Fatalf("invalid message %d accepted: %#v", index, message)
		}
	}
}

func TestMessageCloneDoesNotAliasRecipients(t *testing.T) {
	original := Message{To: []string{"a", "b"}}
	clone := original.Clone()
	clone.To[0] = "changed"
	if original.To[0] != "a" {
		t.Fatalf("clone mutated original: %#v", original)
	}
}

func TestMailgunAdapterUsesInjectedAPI(t *testing.T) {
	api := &fakeMailgunAPI{}
	adapter, err := NewMailgunWithAPI(validMailgunConfig(), api)
	if err != nil {
		t.Fatal(err)
	}
	message := validMessage()
	if err := adapter.Deliver(context.Background(), message); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if api.calls != 1 || !reflect.DeepEqual(api.message, message) {
		t.Fatalf("API call = %d %#v", api.calls, api.message)
	}
	api.message.To[0] = "mutated"
	if message.To[0] != "a@example.test" {
		t.Fatalf("provider mutated caller message: %#v", message)
	}
}

func TestMailgunAdapterTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		deadlineSeen := false
		api := &fakeMailgunAPI{send: func(ctx context.Context) error {
			_, deadlineSeen = ctx.Deadline()
			return context.DeadlineExceeded
		}}
		adapter, err := NewMailgunWithAPI(validMailgunConfig(), api)
		if err != nil {
			t.Fatal(err)
		}
		err = adapter.Deliver(context.Background(), validMessage())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}
		if !deadlineSeen {
			t.Fatal("Mailgun request context has no deadline")
		}
	})
	t.Run("parent cancellation", func(t *testing.T) {
		api := &fakeMailgunAPI{send: func(ctx context.Context) error {
			return ctx.Err()
		}}
		adapter, err := NewMailgunWithAPI(validMailgunConfig(), api)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = adapter.Deliver(ctx, validMessage())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	})
}

func TestMailgunAdapterErrorsAndConfiguration(t *testing.T) {
	secret := "super-secret-key"
	providerErr := errors.New("provider unavailable using " + secret)
	api := &fakeMailgunAPI{err: providerErr}
	config := validMailgunConfig()
	config.APIKey = secret
	adapter, err := NewMailgunWithAPI(config, api)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Deliver(context.Background(), validMessage())
	if !errors.Is(err, providerErr) || strings.Contains(err.Error(), secret) {
		t.Fatalf("provider error = %v", err)
	}
	if err := adapter.Deliver(context.Background(), Message{}); err == nil || api.calls != 1 {
		t.Fatalf("invalid message error=%v calls=%d", err, api.calls)
	}

	tests := []MailgunConfig{
		{APIKey: "key", Timeout: time.Second},
		{Domain: "example.test", Timeout: time.Second},
		{Domain: "example.test", APIKey: "key"},
	}
	for _, invalid := range tests {
		if _, err := NewMailgunWithAPI(invalid, api); err == nil {
			t.Fatalf("accepted invalid config %#v", invalid)
		}
	}
	if _, err := NewMailgunWithAPI(validMailgunConfig(), nil); err == nil {
		t.Fatal("accepted nil API")
	}
	production, err := NewMailgun(validMailgunConfig())
	if err != nil || production == nil {
		t.Fatalf("NewMailgun = %#v, %v", production, err)
	}
}

type fakeMailgunAPI struct {
	calls   int
	message Message
	err     error
	send    func(context.Context) error
}

func (api *fakeMailgunAPI) Send(ctx context.Context, message Message) error {
	api.calls++
	api.message = message
	if api.send != nil {
		return api.send(ctx)
	}
	return api.err
}

func validMailgunConfig() MailgunConfig {
	return MailgunConfig{Domain: "mg.example.test", APIKey: "key", Timeout: time.Second}
}

func validMessage() Message {
	return Message{
		From:    "Org Mealbot <bot@example.test>",
		To:      []string{"a@example.test", "b@example.test"},
		Subject: EmailSubject,
		Text:    "body",
	}
}
