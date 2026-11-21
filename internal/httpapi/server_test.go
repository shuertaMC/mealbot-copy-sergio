package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnamadeo/mealbot/internal/auth"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/httpapi/handlers"
	"github.com/johnamadeo/mealbot/internal/mail"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	static := t.TempDir()
	if err := os.WriteFile(filepath.Join(static, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// The JWKS URL is never contacted: every request below is rejected (or
	// answered) before a key is needed.
	authMW := auth.NewMiddleware(ctx, auth.Config{
		Issuer:   "iss",
		Audience: "aud",
		JWKSURL:  "http://127.0.0.1:1/jwks.json",
	})
	h := handlers.New(nil, &mail.Fake{}, config.Config{})
	return NewRouter(h, authMW, static)
}

func TestHealthzIsUnauthenticatedAndNeedsNoDB(t *testing.T) {
	router := newTestRouter(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %q", ct)
	}
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	router := newTestRouter(t)
	for _, path := range []string{"/members?org=o", "/orgs?admin=a", "/org", "/crossmatchtrait", "/rounds", "/round", "/pairs"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: code = %d, want 401", path, rec.Code)
		}
	}
}

func TestPreflightBypassesAuthAndGetsCORSHeaders(t *testing.T) {
	router := newTestRouter(t)
	req := httptest.NewRequest("OPTIONS", "/members", nil)
	req.Header.Set("Origin", "http://example.test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.test" {
		t.Fatalf("headers: %v", rec.Header())
	}
}

func TestUnauthorizedResponsesCarryCORSHeaders(t *testing.T) {
	router := newTestRouter(t)
	req := httptest.NewRequest("GET", "/members?org=o", nil)
	req.Header.Set("Origin", "http://example.test")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.test" {
		t.Fatalf("headers: %v", rec.Header())
	}
}

func TestStaticFilesServedAtRoot(t *testing.T) {
	router := newTestRouter(t)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/hello.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "hi" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestChainOrder(t *testing.T) {
	order := []string{}
	mk := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	core := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { order = append(order, "core") })
	Chain(core, mk("inner"), mk("outer")).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	want := []string{"outer", "inner", "core"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestServeShutsDownGracefully(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", Health)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, addr, mux) }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}
