package mail

import (
	"context"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
)

func TestBodyAndFrom(t *testing.T) {
	body := Body([]string{"Ann", "Bob"})
	if !strings.HasPrefix(body, EmailIntro+" \nAnn\r\nBob\n\n ") || !strings.HasSuffix(body, EmailFooter) {
		t.Errorf("unexpected body: %q", body)
	}
	if got := From("ysc", "bot@example.com"); got != "ysc Mealbot <bot@example.com>" {
		t.Errorf("From = %q", got)
	}
}

func TestSendRequiresConfiguration(t *testing.T) {
	err := NewMailgun(config.Mailgun{}).SendGroupEmail(context.Background(), "ysc", []string{"a@x.com"}, []string{"A"})
	if err == nil || !strings.Contains(err.Error(), "MAILGUN_SMTP_LOGIN") {
		t.Errorf("expected missing MAILGUN_SMTP_LOGIN error, got %v", err)
	}
}
