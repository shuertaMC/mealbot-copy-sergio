// Package mail sends the "your new Mealbot group" emails through Mailgun.
package mail

import (
	"context"
	"fmt"
	"strings"
	"time"

	mailgun "github.com/mailgun/mailgun-go/v4"

	"github.com/johnamadeo/mealbot/internal/config"
)

const (
	// EmailIntro : Text to put in the beginning of the email body
	EmailIntro = "Your Mealbot group this week is:"
	// EmailFooter : Text to put at the end of the email body
	EmailFooter = "Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com"
	// EmailSubject : Subject of the email
	EmailSubject = "Your new Mealbot group"

	sendTimeout = 30 * time.Second
)

// Sender delivers the email announcing a new group. It is an interface so the
// pairing code can be tested without talking to Mailgun.
type Sender interface {
	SendGroupEmail(ctx context.Context, orgname string, toEmails []string, toNames []string) error
}

// Body renders the text of the email for the given member names.
func Body(toNames []string) string {
	return fmt.Sprintf(
		"%s \n%s\n\n %s",
		EmailIntro,
		strings.Join(toNames, "\r\n"),
		EmailFooter,
	)
}

// From renders the sender line for an organization.
func From(orgname string, smtpLogin string) string {
	return fmt.Sprintf("%s Mealbot <%s>", orgname, smtpLogin)
}

// Mailgun is a Sender backed by the Mailgun API.
type Mailgun struct {
	cfg config.Mailgun
}

// NewMailgun creates a Sender using the given Mailgun settings.
func NewMailgun(cfg config.Mailgun) *Mailgun {
	return &Mailgun{cfg: cfg}
}

// SendGroupEmail queues one email to all toEmails on Mailgun.
func (m *Mailgun) SendGroupEmail(ctx context.Context, orgname string, toEmails []string, toNames []string) error {
	settings := config.Config{Mailgun: m.cfg}
	if err := settings.ValidateMailgun(); err != nil {
		return err
	}

	mg := mailgun.NewMailgun(m.cfg.Domain, m.cfg.APIKey)
	message := mg.NewMessage(
		From(orgname, m.cfg.SMTPLogin),
		EmailSubject,
		Body(toNames),
		toEmails...,
	)

	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	_, _, err := mg.Send(ctx, message)
	return err
}
