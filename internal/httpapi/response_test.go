package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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

func TestResponderCleanupInvokesCallbackAndLogsSafeLabels(t *testing.T) {
	sink := &captureSink{}
	responder := NewResponder(slog.New(captureHandler{sink: sink}))
	called := false
	responder.Cleanup("CreateMembersHandler", "multipart file", func() error {
		called = true
		return errors.New("close failed for /private/upload.csv")
	})
	if !called {
		t.Fatal("cleanup callback was not invoked")
	}
	if len(sink.records) != 1 {
		t.Fatalf("cleanup records = %d, want 1", len(sink.records))
	}
	got := sink.records[0]
	if got.message != "resource cleanup failed" || got.level != slog.LevelError {
		t.Fatalf("cleanup record = %#v", got)
	}
	wantAttrs := map[string]any{
		"function": "CreateMembersHandler",
		"resource": "multipart file",
	}
	if !reflect.DeepEqual(got.attrs, wantAttrs) {
		t.Fatalf("cleanup attributes = %#v, want %#v", got.attrs, wantAttrs)
	}
	logged := fmt.Sprint(sink.records)
	for _, sensitive := range []string{"close failed", "/private/upload.csv"} {
		if strings.Contains(logged, sensitive) {
			t.Fatalf("cleanup log contains sensitive value %q: %s", sensitive, logged)
		}
	}

	called = false
	NewResponder(nil).Cleanup("CreateMembersHandler", "multipart form", func() error {
		called = true
		return errors.New("remove failed")
	})
	if !called {
		t.Fatal("cleanup callback was not invoked without a logger")
	}
}

func TestResponderCleanupDefersCloseBeforeRemoveAfterFailures(t *testing.T) {
	sink := &captureSink{}
	responder := NewResponder(slog.New(captureHandler{sink: sink}))
	var events []string
	func() {
		defer responder.Cleanup("CreateMembersHandler", "multipart form", func() error {
			events = append(events, "remove")
			return errors.New("remove failed")
		})
		defer responder.Cleanup("CreateMembersHandler", "multipart file", func() error {
			events = append(events, "close")
			return errors.New("close failed")
		})
	}()
	if want := []string{"close", "remove"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("cleanup order = %v, want %v", events, want)
	}
	if len(sink.records) != 2 {
		t.Fatalf("cleanup records = %d, want 2", len(sink.records))
	}
}
