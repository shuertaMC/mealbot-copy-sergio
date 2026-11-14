package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/logging"
)

func TestVersionEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		wantStatus  int
		wantBody    string
		wantContent string
		wantAllow   string
	}{
		{
			name:        "GET returns version with default dev",
			method:      "GET",
			wantStatus:  http.StatusOK,
			wantBody:    `{"version":"dev"}`,
			wantContent: "application/json",
		},
		{
			name:        "GET returns version with custom value",
			method:      "GET",
			wantStatus:  http.StatusOK,
			wantBody:    `{"version":"1.2.3"}`,
			wantContent: "application/json",
		},
		{
			name:       "POST returns 405 with Allow header",
			method:     "POST",
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   `{"Message":"Only GET requests are allowed at this route"}`,
			wantAllow:  "GET",
		},
		{
			name:       "PUT returns 405 with Allow header",
			method:     "PUT",
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   `{"Message":"Only GET requests are allowed at this route"}`,
			wantAllow:  "GET",
		},
		{
			name:       "DELETE returns 405 with Allow header",
			method:     "DELETE",
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   `{"Message":"Only GET requests are allowed at this route"}`,
			wantAllow:  "GET",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Save and restore config.Version after the custom version test
			origVersion := config.Version
			if tc.name == "GET returns version with custom value" {
				config.Version = "1.2.3"
				defer func() { config.Version = origVersion }()
			}

			srv := NewServer(nil, config.Config{}, logging.Discard())
			h := srv.Routes(Middleware{})

			req := httptest.NewRequest(tc.method, "/version", nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantContent != "" && rec.Header().Get("Content-Type") != tc.wantContent {
				t.Errorf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), tc.wantContent)
			}
			if rec.Body.String() != tc.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tc.wantBody)
			}
			if tc.wantAllow != "" && rec.Header().Get("Allow") != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", rec.Header().Get("Allow"), tc.wantAllow)
			}
		})
	}
}