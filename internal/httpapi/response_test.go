package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponderJSONMarshalFailure(t *testing.T) {
	t.Parallel()
	response := httptest.NewRecorder()

	newResponder(testLogger()).json(response, http.StatusOK, make(chan struct{}))

	assertHTTPResponse(t, response, http.StatusInternalServerError, "{\"error\":\"internal server error\"}\n", "application/json; charset=utf-8")
	if strings.Contains(response.Body.String(), "unsupported") || strings.Contains(response.Body.String(), "chan struct") {
		t.Fatalf("response exposed JSON encoding details: %q", response.Body.String())
	}
}
