package mailer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestComposeGroupPreservesEnvelopeAndBody(t *testing.T) {
	group := []Recipient{
		{Email: "a@example.test", Name: "Ada"},
		{Email: "b@example.test", Name: "Bob"},
		{Email: "c@example.test", Name: "Cy"},
	}
	message, err := ComposeGroup("Acme", "bot@example.test", group)
	if err != nil {
		t.Fatalf("ComposeGroup: %v", err)
	}
	if message.From != "Acme Mealbot <bot@example.test>" || message.Subject != EmailSubject {
		t.Fatalf("envelope = %#v", message)
	}
	if !reflect.DeepEqual(message.To, []string{"a@example.test", "b@example.test", "c@example.test"}) {
		t.Fatalf("recipients = %v", message.To)
	}
	wantBody := EmailIntro + " \nAda\r\nBob\r\nCy\n\n " + EmailFooter
	if message.Text != wantBody {
		t.Fatalf("body = %q, want %q", message.Text, wantBody)
	}
}

func TestComposeGroupAndMessageValidation(t *testing.T) {
	valid := []Recipient{{Email: "a@example.test", Name: "A"}, {Email: "b@example.test", Name: "B"}}
	tests := []struct {
		name  string
		org   string
		smtp  string
		group []Recipient
	}{
		{name: "organization empty", smtp: "bot@example.test", group: valid},
		{name: "organization header injection", org: "org\nBCC: x", smtp: "bot@example.test", group: valid},
		{name: "smtp empty", org: "org", group: valid},
		{name: "smtp header injection", org: "org", smtp: "bot\r@example.test", group: valid},
		{name: "too few", org: "org", smtp: "bot@example.test", group: valid[:1]},
		{name: "too many", org: "org", smtp: "bot@example.test", group: append(valid, valid...)},
		{name: "email empty", org: "org", smtp: "bot@example.test", group: []Recipient{{Name: "A"}, valid[1]}},
		{name: "name empty", org: "org", smtp: "bot@example.test", group: []Recipient{{Email: "a@example.test"}, valid[1]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ComposeGroup(test.org, test.smtp, test.group); err == nil {
				t.Fatal("ComposeGroup accepted invalid input")
			}
		})
	}

	messageTests := []Message{
		{To: []string{"a", "b"}, Subject: "s", Text: "t"},
		{From: "from\n", To: []string{"a", "b"}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", "b"}, Text: "t"},
		{From: "from", To: []string{"a"}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", "b", "c", "d"}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", ""}, Subject: "s", Text: "t"},
		{From: "from", To: []string{"a", "b"}, Subject: "s"},
	}
	for index, message := range messageTests {
		if err := message.Validate(); err == nil {
			t.Fatalf("invalid message %d accepted: %#v", index, message)
		}
	}
}

func TestMessageCloneDoesNotAliasRecipients(t *testing.T) {
	original := Message{To: []string{"a", "b"}}
	clone := original.Clone()
	clone.To[0] = "changed"
	if original.To[0] != "a" {
		t.Fatalf("clone mutated original: %#v", original)
	}
}

func TestMailgunAdapterUsesInjectedAPI(t *testing.T) {
	api := &fakeMailgunAPI{}
	adapter, err := NewMailgunWithAPI(validMailgunConfig(), api)
	if err != nil {
		t.Fatal(err)
	}
	message := validMessage()
	if err := adapter.Deliver(context.Background(), message); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if api.calls != 1 || !reflect.DeepEqual(api.message, message) {
		t.Fatalf("API call = %d %#v", api.calls, api.message)
	}
	api.message.To[0] = "mutated"
	if message.To[0] != "a@example.test" {
		t.Fatalf("provider mutated caller message: %#v", message)
	}
}

func TestMailgunProductionSDKSendPath(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		wantError  string
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			response:   `{"message":"Queued. Thank you.","id":"<message-id@mg.example.test>"}`,
		},
		{
			name:       "provider error",
			statusCode: http.StatusBadGateway,
			response:   `{"message":"provider unavailable"}`,
			wantError:  "mailgun send message failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := validMessage()
			server := httptest.NewServer(http.StripPrefix("/v3", http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost {
					t.Errorf("method = %q, want %q", request.Method, http.MethodPost)
				}
				if request.URL.Path != "/mg.example.test/messages" {
					t.Errorf("path = %q, want %q", request.URL.Path, "/mg.example.test/messages")
				}
				username, password, ok := request.BasicAuth()
				if !ok || username != "api" || password != "key" {
					t.Errorf("basic auth = %q, %q, %t; want %q, %q, true", username, password, ok, "api", "key")
				}
				if err := request.ParseMultipartForm(1 << 20); err != nil {
					t.Errorf("ParseMultipartForm: %v", err)
					response.WriteHeader(http.StatusBadRequest)
					return
				}
				if got := request.MultipartForm.Value["from"]; !reflect.DeepEqual(got, []string{message.From}) {
					t.Errorf("from = %v, want %v", got, []string{message.From})
				}
				if got := request.MultipartForm.Value["to"]; !reflect.DeepEqual(got, message.To) {
					t.Errorf("to = %v, want %v", got, message.To)
				}
				if got := request.MultipartForm.Value["subject"]; !reflect.DeepEqual(got, []string{message.Subject}) {
					t.Errorf("subject = %v, want %v", got, []string{message.Subject})
				}
				if got := request.MultipartForm.Value["text"]; !reflect.DeepEqual(got, []string{message.Text}) {
					t.Errorf("text = %v, want %v", got, []string{message.Text})
				}
				response.WriteHeader(test.statusCode)
				if _, err := response.Write([]byte(test.response)); err != nil {
					t.Errorf("write response: %v", err)
				}
			})))
			defer server.Close()

			adapter, err := NewMailgun(validMailgunConfig())
			if err != nil {
				t.Fatalf("NewMailgun: %v", err)
			}
			productionAPI, ok := adapter.api.(mailgunV4API)
			if !ok {
				t.Fatalf("NewMailgun API = %T, want mailgunV4API", adapter.api)
			}
			productionAPI.client.SetAPIBase(server.URL + "/v3")

			err = adapter.Deliver(context.Background(), message)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Deliver: %v (cause: %v)", err, errors.Unwrap(err))
				}
				return
			}
			if err == nil || err.Error() != test.wantError {
				t.Fatalf("Deliver error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestMailgunAdapterTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		deadlineSeen := false
		api := &fakeMailgunAPI{send: func(ctx context.Context) error {
			_, deadlineSeen = ctx.Deadline()
			return context.DeadlineExceeded
		}}
		adapter, err := NewMailgunWithAPI(validMailgunConfig(), api)
		if err != nil {
			t.Fatal(err)
		}
		err = adapter.Deliver(context.Background(), validMessage())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timeout error = %v", err)
		}
		if !deadlineSeen {
			t.Fatal("Mailgun request context has no deadline")
		}
	})
	t.Run("parent cancellation", func(t *testing.T) {
		api := &fakeMailgunAPI{send: func(ctx context.Context) error {
			return ctx.Err()
		}}
		adapter, err := NewMailgunWithAPI(validMailgunConfig(), api)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = adapter.Deliver(ctx, validMessage())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v", err)
		}
	})
}

func TestMailgunAdapterErrorsAndConfiguration(t *testing.T) {
	secret := "super-secret-key"
	providerErr := errors.New("provider unavailable using " + secret)
	api := &fakeMailgunAPI{err: providerErr}
	config := validMailgunConfig()
	config.APIKey = secret
	adapter, err := NewMailgunWithAPI(config, api)
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.Deliver(context.Background(), validMessage())
	if !errors.Is(err, providerErr) || strings.Contains(err.Error(), secret) {
		t.Fatalf("provider error = %v", err)
	}
	if err := adapter.Deliver(context.Background(), Message{}); err == nil || api.calls != 1 {
		t.Fatalf("invalid message error=%v calls=%d", err, api.calls)
	}

	tests := []MailgunConfig{
		{APIKey: "key", Timeout: time.Second},
		{Domain: "example.test", Timeout: time.Second},
		{Domain: "example.test", APIKey: "key"},
	}
	for _, invalid := range tests {
		if _, err := NewMailgunWithAPI(invalid, api); err == nil {
			t.Fatalf("accepted invalid config %#v", invalid)
		}
	}
	if _, err := NewMailgunWithAPI(validMailgunConfig(), nil); err == nil {
		t.Fatal("accepted nil API")
	}
	production, err := NewMailgun(validMailgunConfig())
	if err != nil || production == nil {
		t.Fatalf("NewMailgun = %#v, %v", production, err)
	}
}

type fakeMailgunAPI struct {
	calls   int
	message Message
	err     error
	send    func(context.Context) error
}

func (api *fakeMailgunAPI) Send(ctx context.Context, message Message) error {
	api.calls++
	api.message = message
	if api.send != nil {
		return api.send(ctx)
	}
	return api.err
}

func validMailgunConfig() MailgunConfig {
	return MailgunConfig{Domain: "mg.example.test", APIKey: "key", Timeout: time.Second}
}

func validMessage() Message {
	return Message{
		From:    "Org Mealbot <bot@example.test>",
		To:      []string{"a@example.test", "b@example.test"},
		Subject: EmailSubject,
		Text:    "body",
	}
}
