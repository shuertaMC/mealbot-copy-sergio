package httpapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMessageAndErrorBytesPreserveLegacyEnvelope(t *testing.T) {
	if got, want := string(MessageBytes("created")), `{"Message":"created"}`; got != want {
		t.Fatalf("MessageBytes() = %q, want %q", got, want)
	}
	if got, want := string(ErrorBytes(errors.New("failed"))), `{"Message":"failed"}`; got != want {
		t.Fatalf("ErrorBytes() = %q, want %q", got, want)
	}
}

func TestResponderPreservesSuccessAndErrorStatusAndContentType(t *testing.T) {
	responder := NewResponder(nil)

	success := httptest.NewRecorder()
	responder.Write(success, MessageBytes("created"), http.StatusCreated, "Create")
	if success.Code != http.StatusOK {
		t.Fatalf("success status = %d, want implicit 200", success.Code)
	}
	if got := success.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("success Content-Type = %q", got)
	}
	if got := success.Body.String(); got != `{"Message":"created"}` {
		t.Fatalf("success body = %q", got)
	}

	failure := httptest.NewRecorder()
	responder.WriteError(failure, errors.New("failed"), http.StatusBadRequest, "Create")
	if failure.Code != http.StatusBadRequest {
		t.Fatalf("error status = %d", failure.Code)
	}
	if got := failure.Header().Get("Content-Type"); got != "" {
		t.Fatalf("error Content-Type = %q, want empty", got)
	}
	if got := failure.Body.String(); got != `{"Message":"failed"}` {
		t.Fatalf("error body = %q", got)
	}
}
