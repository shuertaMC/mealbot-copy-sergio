package mailer

import (
	"context"
	"errors"
	"strings"
	"time"

	mailgunv4 "github.com/mailgun/mailgun-go/v4"
)

// MailgunConfig contains injected provider settings. APIKey is retained only
// by the v4 client and is never included in adapter-generated errors.
type MailgunConfig struct {
	Domain  string
	APIKey  string
	Timeout time.Duration
}

// MailgunAPI is the provider seam used by the adapter. Its Message argument
// remains independent of Mailgun SDK types, allowing isolated tests.
type MailgunAPI interface {
	Send(ctx context.Context, message Message) error
}

// Mailgun delivers messages through a configured API with a per-call timeout.
type Mailgun struct {
	timeout time.Duration
	api     MailgunAPI
}

// NewMailgun constructs the production Mailgun v4 adapter.
func NewMailgun(config MailgunConfig) (*Mailgun, error) {
	if err := validateMailgunConfig(config); err != nil {
		return nil, err
	}
	client := mailgunv4.NewMailgun(strings.TrimSpace(config.Domain), strings.TrimSpace(config.APIKey))
	return NewMailgunWithAPI(config, mailgunV4API{client: client})
}

// NewMailgunWithAPI constructs an adapter around an injected provider seam.
// It is intended for tests and alternate transports that preserve semantics.
func NewMailgunWithAPI(config MailgunConfig, api MailgunAPI) (*Mailgun, error) {
	if err := validateMailgunConfig(config); err != nil {
		return nil, err
	}
	if api == nil {
		return nil, errors.New("mailer mailgun: API client is required")
	}
	return &Mailgun{timeout: config.Timeout, api: api}, nil
}

// Deliver validates and defensively copies the message, applies the configured
// timeout, and wraps provider failures without adding credentials.
func (m *Mailgun) Deliver(ctx context.Context, message Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	requestContext, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	if err := m.api.Send(requestContext, message.Clone()); err != nil {
		return &providerError{cause: err}
	}
	return nil
}

// providerError preserves errors.Is/errors.As access to the provider cause
// while keeping its potentially credential-bearing text out of user-visible
// error strings.
type providerError struct {
	cause error
}

func (e *providerError) Error() string { return "mailgun send message failed" }

func (e *providerError) Unwrap() error { return e.cause }

func validateMailgunConfig(config MailgunConfig) error {
	if strings.TrimSpace(config.Domain) == "" {
		return errors.New("mailer mailgun: domain is required")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return errors.New("mailer mailgun: API key is required")
	}
	if config.Timeout <= 0 {
		return errors.New("mailer mailgun: timeout must be positive")
	}
	return nil
}

type mailgunV4API struct {
	client *mailgunv4.MailgunImpl
}

func (api mailgunV4API) Send(ctx context.Context, message Message) error {
	providerMessage := mailgunv4.NewMessage(message.From, message.Subject, message.Text, message.To...)
	_, _, err := api.client.Send(ctx, providerMessage)
	return err
}
