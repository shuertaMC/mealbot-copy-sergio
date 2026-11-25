package mailer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	pairingdomain "github.com/johnamadeo/mealbot/internal/pairing"
	mailgun "github.com/mailgun/mailgun-go/v5"
)

const (
	// EmailIntro is the compatibility introduction used by the legacy service.
	EmailIntro = "Your Mealbot group this week is:"
	// EmailFooter is the compatibility footer used by the legacy service.
	EmailFooter = "Feel free to reply all in this thread for scheduling. I'm a robot, so I can only read 1's and 0's.\n\n Sent by your friendly neighborhood Mealbot! Learn more about me at https://mealbot-web.herokuapp.com"
	// EmailSubject is the compatibility subject used by the legacy service.
	EmailSubject = "Your new Mealbot group"

	// DefaultTimeout bounds each provider request when no shorter timeout is configured.
	DefaultTimeout = 10 * time.Second
)

var (
	// ErrDelivery deliberately contains no provider response or message data.
	ErrDelivery = errors.New("mail delivery failed")
	// ErrInvalidPair identifies calls which do not describe exactly one pair.
	ErrInvalidPair = errors.New("mail notification requires exactly two recipients and two names")
)

// Config contains the values needed by one Mailgun adapter. It never reads the
// process environment; composition roots pass an already-loaded snapshot.
type Config struct {
	Domain    string
	APIKey    string
	SMTPLogin string
	Timeout   time.Duration
}

// Adapter delivers compatibility pair notifications through Mailgun.
// Its immutable fields and per-call messages are safe for concurrent use.
type Adapter struct {
	domain    string
	smtpLogin string
	timeout   time.Duration
	sender    Sender
}

var _ pairingdomain.Notifier = (*Adapter)(nil)

// New constructs a Mailgun v5-backed notification adapter.
func New(config Config) (*Adapter, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	client := mailgun.NewMailgun(config.APIKey)
	sender := SenderFunc(func(ctx context.Context, message Message) error {
		providerMessage := mailgun.NewMessage(
			message.Domain,
			message.From,
			message.Subject,
			message.Text,
			message.To...,
		)
		_, err := client.Send(ctx, providerMessage)
		return err
	})
	return newAdapter(config, sender), nil
}

// NewWithSender constructs an adapter with an injected provider seam. It is
// intended for deterministic tests and alternate provider transports.
func NewWithSender(config Config, sender Sender) (*Adapter, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if sender == nil {
		return nil, errors.New("mailer sender is required")
	}
	return newAdapter(config, sender), nil
}

func newAdapter(config Config, sender Sender) *Adapter {
	timeout := config.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Adapter{
		domain:    config.Domain,
		smtpLogin: config.SMTPLogin,
		timeout:   timeout,
		sender:    sender,
	}
}

func validateConfig(config Config) error {
	for _, required := range []struct {
		name  string
		value string
	}{
		{name: "MAILGUN_DOMAIN", value: config.Domain},
		{name: "MAILGUN_API_KEY", value: config.APIKey},
		{name: "MAILGUN_SMTP_LOGIN", value: config.SMTPLogin},
	} {
		if strings.TrimSpace(required.value) == "" {
			return fmt.Errorf("mailer requires setting %s", required.name)
		}
	}
	if config.Timeout < 0 {
		return errors.New("mailer timeout must not be negative")
	}
	return nil
}

// ComposeMessage creates the exact provider-independent compatibility message.
// Recipient input is copied so later caller mutation cannot affect delivery.
func ComposeMessage(domain, smtpLogin, organization string, emails, names []string) Message {
	recipients := append([]string(nil), emails...)
	return Message{
		Domain:  domain,
		From:    fmt.Sprintf("%s Mealbot <%s>", organization, smtpLogin),
		To:      recipients,
		Subject: EmailSubject,
		Text: fmt.Sprintf(
			"%s \n%s\n\n %s",
			EmailIntro,
			strings.Join(names, "\r\n"),
			EmailFooter,
		),
	}
}

// Notify sends exactly one pair notification using both recipients and names.
func (a *Adapter) Notify(ctx context.Context, organization string, emails, names []string) error {
	if ctx == nil {
		return errors.New("mailer context is required")
	}
	if len(emails) != 2 || len(names) != 2 {
		return ErrInvalidPair
	}
	if err := ctx.Err(); err != nil {
		return deliveryContextError(err)
	}

	requestContext, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	message := ComposeMessage(a.domain, a.smtpLogin, organization, emails, names)
	if err := a.sender.Send(requestContext, message); err != nil {
		if requestErr := requestContext.Err(); requestErr != nil {
			return deliveryContextError(requestErr)
		}
		if errors.Is(err, context.Canceled) {
			return deliveryContextError(context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return deliveryContextError(context.DeadlineExceeded)
		}
		return ErrDelivery
	}
	if requestErr := requestContext.Err(); requestErr != nil {
		return deliveryContextError(requestErr)
	}
	return nil
}

func deliveryContextError(err error) error {
	return fmt.Errorf("%w: %w", ErrDelivery, err)
}
