// Package mail sends email through Mailgun behind a small interface so that
// tests can use a fake.
package mail

import (
	"context"
	"errors"

	"github.com/johnamadeo/mealbot/internal/config"
	mailgun "github.com/mailgun/mailgun-go/v4"
)

// Message is an outgoing plain text email.
type Message struct {
	From    string
	Subject string
	Text    string
	To      []string
}

// Mailer sends emails.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

// Mailgun is a Mailer backed by the Mailgun API.
type Mailgun struct {
	domain string
	apiKey string
}

// NewMailgun creates a Mailgun mailer. Missing credentials are reported when
// Send is called, not at construction time.
func NewMailgun(cfg config.Mailgun) *Mailgun {
	return &Mailgun{domain: cfg.Domain, apiKey: cfg.APIKey}
}

// Send implements Mailer.
func (m *Mailgun) Send(ctx context.Context, msg Message) error {
	if m.domain == "" {
		return errors.New("environment variable MAILGUN_DOMAIN not set")
	}
	if m.apiKey == "" {
		return errors.New("environment variable MAILGUN_API_KEY not set")
	}

	mg := mailgun.NewMailgun(m.domain, m.apiKey)
	message := mg.NewMessage(msg.From, msg.Subject, msg.Text, msg.To...)

	_, _, err := mg.Send(ctx, message)
	return err
}

// Fake is a Mailer that records messages; intended for tests.
type Fake struct {
	Sent []Message
	Err  error
}

// Send implements Mailer.
func (f *Fake) Send(_ context.Context, msg Message) error {
	if f.Err != nil {
		return f.Err
	}
	f.Sent = append(f.Sent, msg)
	return nil
}
