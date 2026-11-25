package mailer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		Domain:    "mg.example.test",
		APIKey:    "api-secret",
		SMTPLogin: "mealbot@mg.example.test",
		Timeout:   time.Second,
	}
}

func TestComposeMessageExactCompatibilityContent(t *testing.T) {
	emails := []string{"ada@example.test", "grace@example.test"}
	names := []string{"Ada Lovelace", "Grace Hopper"}

	message := ComposeMessage("mg.example.test", "bot@mg.example.test", "Engineering", emails, names)

	want := Message{
		Domain:  "mg.example.test",
		From:    "Engineering Mealbot <bot@mg.example.test>",
		To:      []string{"ada@example.test", "grace@example.test"},
		Subject: "Your new Mealbot group",
		Text: "Your Mealbot group this week is: \n" +
			"Ada Lovelace\r\nGrace Hopper\n\n " +
			"Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n " +
			"Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com",
	}
	if !reflect.DeepEqual(message, want) {
		t.Fatalf("message mismatch\n got: %#v\nwant: %#v", message, want)
	}

	emails[0] = "mutated@example.test"
	names[0] = "Mutated"
	if message.To[0] != want.To[0] || message.Text != want.Text {
		t.Fatalf("composed message changed after input mutation: %#v", message)
	}
}

func TestNewWithSenderValidatesRequiredConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Config)
		wantError string
	}{
		{
			name: "domain",
			mutate: func(config *Config) {
				config.Domain = " \t "
			},
			wantError: "mailer requires setting MAILGUN_DOMAIN",
		},
		{
			name: "API key",
			mutate: func(config *Config) {
				config.APIKey = ""
			},
			wantError: "mailer requires setting MAILGUN_API_KEY",
		},
		{
			name: "SMTP login",
			mutate: func(config *Config) {
				config.SMTPLogin = ""
			},
			wantError: "mailer requires setting MAILGUN_SMTP_LOGIN",
		},
		{
			name: "negative timeout",
			mutate: func(config *Config) {
				config.Timeout = -time.Nanosecond
			},
			wantError: "mailer timeout must not be negative",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig()
			test.mutate(&config)
			_, err := NewWithSender(config, SenderFunc(func(context.Context, Message) error { return nil }))
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("error = %v, want %q", err, test.wantError)
			}
			for _, secret := range []string{"api-secret", "mg.example.test", "mealbot@mg.example.test"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("configuration error leaked value %q: %v", secret, err)
				}
			}
		})
	}
}

func TestNewWithSenderRejectsNilSender(t *testing.T) {
	_, err := NewWithSender(validConfig(), nil)
	if err == nil || err.Error() != "mailer sender is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestNotifySendsOneCompletePairWithBoundedContext(t *testing.T) {
	var calls int
	var got Message
	var deadline time.Time
	adapter, err := NewWithSender(validConfig(), SenderFunc(func(ctx context.Context, message Message) error {
		calls++
		got = message
		var ok bool
		deadline, ok = ctx.Deadline()
		if !ok {
			t.Fatal("provider context has no deadline")
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	before := time.Now()
	err = adapter.Notify(
		context.Background(),
		"Platform",
		[]string{"one@example.test", "two@example.test"},
		[]string{"One", "Two"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
	if deadline.Before(before) || deadline.After(before.Add(validConfig().Timeout+100*time.Millisecond)) {
		t.Fatalf("provider deadline %v is outside configured bound", deadline)
	}
	want := ComposeMessage(
		validConfig().Domain,
		validConfig().SMTPLogin,
		"Platform",
		[]string{"one@example.test", "two@example.test"},
		[]string{"One", "Two"},
	)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("message = %#v, want %#v", got, want)
	}
}

func TestNotifyRejectsAnythingOtherThanOnePair(t *testing.T) {
	var calls atomic.Int32
	adapter, err := NewWithSender(validConfig(), SenderFunc(func(context.Context, Message) error {
		calls.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		emails []string
		names  []string
	}{
		{emails: []string{"one@example.test"}, names: []string{"One"}},
		{emails: []string{"one@example.test", "two@example.test"}, names: []string{"One"}},
		{emails: []string{"one@example.test", "two@example.test", "three@example.test"}, names: []string{"One", "Two", "Three"}},
	} {
		if err := adapter.Notify(context.Background(), "Platform", test.emails, test.names); !errors.Is(err, ErrInvalidPair) {
			t.Fatalf("Notify(%d emails, %d names) error = %v", len(test.emails), len(test.names), err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("provider called %d times for invalid pairs", calls.Load())
	}
}

func TestNotifySanitizesProviderError(t *testing.T) {
	providerDetail := "api-secret mg.example.test alice@example.test Alice compatibility body"
	adapter, err := NewWithSender(validConfig(), SenderFunc(func(context.Context, Message) error {
		return errors.New(providerDetail)
	}))
	if err != nil {
		t.Fatal(err)
	}

	err = adapter.Notify(
		context.Background(),
		"Sensitive Organization",
		[]string{"alice@example.test", "bob@example.test"},
		[]string{"Alice", "Bob"},
	)
	if !errors.Is(err, ErrDelivery) || err.Error() != ErrDelivery.Error() {
		t.Fatalf("error = %v, want generic delivery error", err)
	}
	for _, privateValue := range []string{
		providerDetail,
		"api-secret",
		"mg.example.test",
		"alice@example.test",
		"Alice",
		"Sensitive Organization",
		EmailIntro,
	} {
		if strings.Contains(err.Error(), privateValue) {
			t.Fatalf("delivery error leaked %q: %v", privateValue, err)
		}
	}
}

func TestNotifyPropagatesParentCancellation(t *testing.T) {
	started := make(chan struct{})
	adapter, err := NewWithSender(validConfig(), SenderFunc(func(ctx context.Context, _ Message) error {
		close(started)
		<-ctx.Done()
		return errors.New("provider detail: " + ctx.Err().Error())
	}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- adapter.Notify(ctx, "Platform", []string{"one@example.test", "two@example.test"}, []string{"One", "Two"})
	}()
	<-started
	cancel()

	err = <-result
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrDelivery) {
		t.Fatalf("error = %v, want delivery and context cancellation", err)
	}
	if strings.Contains(err.Error(), "provider detail") {
		t.Fatalf("cancellation error leaked provider detail: %v", err)
	}
}

func TestNotifyAppliesInternalTimeout(t *testing.T) {
	config := validConfig()
	config.Timeout = 15 * time.Millisecond
	adapter, err := NewWithSender(config, SenderFunc(func(ctx context.Context, _ Message) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	err = adapter.Notify(context.Background(), "Platform", []string{"one@example.test", "two@example.test"}, []string{"One", "Two"})
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrDelivery) {
		t.Fatalf("error = %v, want delivery deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("internal timeout took %v", elapsed)
	}
}

func TestNotifySanitizesProviderWrappedContextError(t *testing.T) {
	adapter, err := NewWithSender(validConfig(), SenderFunc(func(context.Context, Message) error {
		return fmt.Errorf("recipient and credential detail: %w", context.Canceled)
	}))
	if err != nil {
		t.Fatal(err)
	}

	err = adapter.Notify(context.Background(), "Platform", []string{"one@example.test", "two@example.test"}, []string{"One", "Two"})
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "recipient") {
		t.Fatalf("error = %v, want sanitized cancellation", err)
	}
}

func TestAdapterIsSafeForConcurrentNotifications(t *testing.T) {
	const count = 64
	var mu sync.Mutex
	messages := make(map[string]Message, count)
	adapter, err := NewWithSender(validConfig(), SenderFunc(func(_ context.Context, message Message) error {
		mu.Lock()
		defer mu.Unlock()
		if _, duplicate := messages[message.From]; duplicate {
			return errors.New("duplicate message")
		}
		messages[message.From] = message
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, count)
	for i := 0; i < count; i++ {
		i := i
		wait.Add(1)
		go func() {
			defer wait.Done()
			organization := fmt.Sprintf("Organization %d", i)
			emails := []string{fmt.Sprintf("first%d@example.test", i), fmt.Sprintf("second%d@example.test", i)}
			names := []string{fmt.Sprintf("First %d", i), fmt.Sprintf("Second %d", i)}
			if err := adapter.Notify(context.Background(), organization, emails, names); err != nil {
				errorsFound <- err
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent notification: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(messages) != count {
		t.Fatalf("message count = %d, want %d", len(messages), count)
	}
	for i := 0; i < count; i++ {
		organization := fmt.Sprintf("Organization %d", i)
		from := fmt.Sprintf("%s Mealbot <%s>", organization, validConfig().SMTPLogin)
		message, ok := messages[from]
		if !ok {
			t.Fatalf("missing message from %q", from)
		}
		wantTo := []string{fmt.Sprintf("first%d@example.test", i), fmt.Sprintf("second%d@example.test", i)}
		if !reflect.DeepEqual(message.To, wantTo) {
			t.Fatalf("message %d recipients = %v, want %v", i, message.To, wantTo)
		}
	}
}
