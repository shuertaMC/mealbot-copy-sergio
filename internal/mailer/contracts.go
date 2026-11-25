// Package mailer owns external mail delivery and compatibility message content.
package mailer

import "context"

// Message describes one provider-independent outbound email.
type Message struct {
	Domain  string
	From    string
	To      []string
	Subject string
	Text    string
}

// Sender is the narrow boundary implemented by Mailgun and test fakes.
type Sender interface {
	Send(context.Context, Message) error
}

// SenderFunc adapts a function to Sender.
type SenderFunc func(context.Context, Message) error

// Send calls f.
func (f SenderFunc) Send(ctx context.Context, message Message) error {
	return f(ctx, message)
}
