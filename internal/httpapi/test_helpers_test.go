package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func assertHTTPResponse(t *testing.T, handler http.Handler, method, target string, status int, body, contentType string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d (body %q)", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != body {
		t.Fatalf("body = %q, want %q", got, body)
	}
	if got := recorder.Header().Get("Content-Type"); got != contentType {
		t.Fatalf("Content-Type = %q, want %q", got, contentType)
	}
}

func assertGetOrganizationQueryFailures(t *testing.T, path string, newHandler func() http.Handler) {
	t.Helper()
	tests := []struct {
		name   string
		target string
	}{
		{"missing organization", path},
		{"duplicate organization", path + "?org=one&org=two"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertHTTPResponse(t, newHandler(), http.MethodGet, test.target, http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`, "")
		})
	}
}
