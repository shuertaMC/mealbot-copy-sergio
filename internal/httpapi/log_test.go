package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type capturedRecord struct {
	message string
	level   slog.Level
	attrs   map[string]any
}

type captureSink struct {
	mu      sync.Mutex
	records []capturedRecord
}

type captureHandler struct {
	sink  *captureSink
	attrs []slog.Attr
}

func (h captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h captureHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, len(h.attrs)+record.NumAttrs())
	for _, attr := range h.attrs {
		attrs[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	h.sink.mu.Lock()
	h.sink.records = append(h.sink.records, capturedRecord{message: record.Message, level: record.Level, attrs: attrs})
	h.sink.mu.Unlock()
	return nil
}

func (h captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := captureHandler{sink: h.sink, attrs: append([]slog.Attr{}, h.attrs...)}
	clone.attrs = append(clone.attrs, attrs...)
	return clone
}

func (h captureHandler) WithGroup(string) slog.Handler { return h }

func TestStructuredLogsAreDeterministicAndExcludeSensitiveRequestData(t *testing.T) {
	sink := &captureSink{}
	logger := slog.New(captureHandler{sink: sink})
	responder := NewResponder(logger)
	secretValues := []string{
		"Bearer jwt.secret.value",
		"postgres://user:password@database/mealbot",
		"key-mailgun-secret",
		"alice@example.com",
		`{"cohort":"private"}`,
	}
	errorText := strings.Join(secretValues[1:], " ")
	handler := RequestLogger(logger, "CreateMembersHandler")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		responder.WriteError(w, errors.New(errorText), http.StatusBadRequest, "CreateMembersHandler")
	}))
	req := httptest.NewRequest("POST", "/members?admin=alice@example.com", strings.NewReader(secretValues[4]))
	req.Header.Set("Authorization", secretValues[0])
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if len(sink.records) != 2 {
		t.Fatalf("records = %d, want 2", len(sink.records))
	}
	if got := sink.records[0]; got.message != "http request failed" || got.level != slog.LevelError || got.attrs["function"] != "CreateMembersHandler" || got.attrs["status"] != int64(http.StatusBadRequest) {
		t.Fatalf("error record = %#v", got)
	}
	if got := sink.records[1]; got.message != "http request" || got.level != slog.LevelInfo || got.attrs["function"] != "CreateMembersHandler" || got.attrs["status"] != int64(http.StatusBadRequest) || got.attrs["method"] != "POST" {
		t.Fatalf("request record = %#v", got)
	}
	logged := fmt.Sprint(sink.records)
	for _, secret := range secretValues {
		if strings.Contains(logged, secret) {
			t.Fatalf("logs contain sensitive value %q: %s", secret, logged)
		}
	}
}
