// Package mailer owns external mail delivery and compatibility message content.
package mailer

import "context"

// Message describes one provider-independent outbound email.
type Message struct {
	From    string
	To      []string
	Subject string
	Text    string
}

// Sender is the narrow boundary implemented by Mailgun and test fakes.
type Sender interface {
	Send(context.Context, Message) error
}
