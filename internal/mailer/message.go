package mailer

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// EmailIntro is the first line of every pairing message.
	EmailIntro = "Your Mealbot group this week is:"
	// EmailFooter is the scheduling prompt and legacy Mealbot attribution.
	EmailFooter = "Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com"
	// EmailSubject is the subject of every pairing message.
	EmailSubject = "Your new Mealbot group"
)

// Message is the provider-independent envelope delivered by Delivery.
type Message struct {
	From    string
	To      []string
	Subject string
	Text    string
}

// Clone returns a copy whose recipient slice does not alias the original.
func (m Message) Clone() Message {
	m.To = append([]string(nil), m.To...)
	return m
}

// Validate rejects incomplete envelopes before provider invocation.
func (m Message) Validate() error {
	if strings.TrimSpace(m.From) == "" || strings.ContainsAny(m.From, "\r\n") {
		return errors.New("mailer message: sender is invalid")
	}
	if strings.TrimSpace(m.Subject) == "" || strings.ContainsAny(m.Subject, "\r\n") {
		return errors.New("mailer message: subject is invalid")
	}
	if len(m.To) < 2 || len(m.To) > 3 {
		return errors.New("mailer message: a group must contain two or three recipients")
	}
	for _, recipient := range m.To {
		if strings.TrimSpace(recipient) == "" || strings.ContainsAny(recipient, "\r\n") {
			return errors.New("mailer message: recipient is invalid")
		}
	}
	if m.Text == "" {
		return errors.New("mailer message: body is empty")
	}
	return nil
}

// Recipient supplies the address and display name rendered in group mail.
type Recipient struct {
	Email string
	Name  string
}

// ComposeGroup builds the preserved sender, subject, recipient order, and
// plain-text body layout for a pair or triple.
func ComposeGroup(organization, smtpLogin string, group []Recipient) (Message, error) {
	organization = strings.TrimSpace(organization)
	smtpLogin = strings.TrimSpace(smtpLogin)
	if organization == "" || strings.ContainsAny(organization, "\r\n") {
		return Message{}, errors.New("mailer compose: organization is invalid")
	}
	if smtpLogin == "" || strings.ContainsAny(smtpLogin, "\r\n") {
		return Message{}, errors.New("mailer compose: SMTP login is invalid")
	}
	if len(group) < 2 || len(group) > 3 {
		return Message{}, errors.New("mailer compose: a group must contain two or three members")
	}
	emails := make([]string, 0, len(group))
	names := make([]string, 0, len(group))
	for _, recipient := range group {
		if strings.TrimSpace(recipient.Email) == "" || strings.ContainsAny(recipient.Email, "\r\n") {
			return Message{}, errors.New("mailer compose: member email is invalid")
		}
		if strings.TrimSpace(recipient.Name) == "" || strings.ContainsAny(recipient.Name, "\r\n") {
			return Message{}, errors.New("mailer compose: member name is invalid")
		}
		emails = append(emails, recipient.Email)
		names = append(names, recipient.Name)
	}
	message := Message{
		From:    fmt.Sprintf("%s Mealbot <%s>", organization, smtpLogin),
		To:      emails,
		Subject: EmailSubject,
		Text:    fmt.Sprintf("%s \n%s\n\n %s", EmailIntro, strings.Join(names, "\r\n"), EmailFooter),
	}
	return message, message.Validate()
}

// Delivery sends provider-neutral messages with caller-supplied context.
type Delivery interface {
	Deliver(ctx context.Context, message Message) error
}
