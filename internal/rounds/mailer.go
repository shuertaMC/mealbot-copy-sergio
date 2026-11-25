package rounds

import (
	"errors"
	"fmt"
	"strings"

	mailgun "github.com/mailgun/mailgun-go"

	"github.com/johnamadeo/mealbot/internal/config"
)

const (
	// EmailIntro : Text to put in the beginning of the email body
	EmailIntro = "Your Mealbot group this week is:"
	// EmailFooter : Text to put at the end of the email body
	EmailFooter = "Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com"
	// EmailSubject : Subject of the email
	EmailSubject = "Your new Mealbot group"
)

// Mailer sends the pairing notification for one pair. It is an interface so
// the round runner can be tested without Mailgun.
type Mailer interface {
	SendEmail(orgname string, toEmails []string, toNames []string) error
}

// MailgunMailer is the production Mailer, backed by Mailgun.
type MailgunMailer struct {
	Config config.Mailgun
}

// NewMailgunMailer returns a Mailer using cfg.
func NewMailgunMailer(cfg config.Mailgun) *MailgunMailer {
	return &MailgunMailer{Config: cfg}
}

// SendEmail queues an email to toEmails on Mailgun.
func (m *MailgunMailer) SendEmail(orgname string, toEmails []string, toNames []string) error {
	if m.Config.SMTPLogin == "" {
		return errors.New("environment variable MAILGUN_SMTP_LOGIN not set")
	}
	if m.Config.Domain == "" {
		return errors.New("environment variable MAILGUN_DOMAIN not set")
	}
	if m.Config.APIKey == "" {
		return errors.New("environment variable MAILGUN_API_KEY not set")
	}

	from := fmt.Sprintf("%s Mealbot <%s>", orgname, m.Config.SMTPLogin)
	text := fmt.Sprintf(
		"%s \n%s\n\n %s",
		EmailIntro,
		strings.Join(toNames, "\r\n"),
		EmailFooter,
	)

	mg := mailgun.NewMailgun(m.Config.Domain, m.Config.APIKey)
	message := mg.NewMessage(
		from,
		EmailSubject,
		text,
		toEmails...,
	)

	_, _, err := mg.Send(message)

	if err != nil {
		return err
	}

	fmt.Println("Emails queued on Mailgun!")
	return nil
}
