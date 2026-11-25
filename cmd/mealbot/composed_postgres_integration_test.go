package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/mailer"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store"
)

const composedIntegrationOptIn = "MEALBOT_RUN_POSTGRES_INTEGRATION"

var composedSchemaSequence atomic.Uint64

// TestComposedPostgreSQLJourney proves that the production composition can
// migrate a clean PostgreSQL 19 schema, authenticate real HTTP requests with a
// JWKS-backed RS256 token, synchronize CSV members, schedule and process a
// deterministic round, deliver through the provider-independent mail seam,
// atomically persist the result, and return it through the historical API.
func TestComposedPostgreSQLJourney(t *testing.T) {
	withComposedIntegrationDB(t, func(ctx context.Context, databaseURL string) {
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		const keyID = "composed-integration-key"
		var jwksRequests atomic.Int32
		jwksServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jwksRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
				"kty": "RSA",
				"kid": keyID,
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
			}}})
		}))
		defer jwksServer.Close()

		issuer := jwksServer.URL + "/"
		audience := "https://mealbot.integration.test/"
		cfg := config.Config{
			DatabaseURL:        databaseURL,
			AuthIssuer:         issuer,
			AuthAudience:       audience,
			AuthJWKSURL:        jwksServer.URL + "/.well-known/jwks.json",
			ReflectCORSOrigin:  true,
			CORSAllowedHeaders: config.DefaultCORSAllowedHeaders,
			CORSAllowedMethods: config.DefaultCORSAllowedMethods,
			MailgunDomain:      "mg.integration.test",
			MailgunAPIKey:      "not-a-real-key",
			MailgunSMTPLogin:   "mealbot@mg.integration.test",
		}
		if err := cfg.ValidateForCommand(config.CommandPair); err != nil {
			t.Fatalf("validate integration configuration: %v", err)
		}

		deps := productionDependencies()
		deps.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
		db, repository, err := openApplicationStore(ctx, cfg, deps)
		if err != nil {
			t.Fatalf("open production application store: %v", err)
		}
		defer db.Close()

		var migrationCount int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
			t.Fatalf("read migration ledger: %v", err)
		}
		if migrationCount != 3 {
			t.Fatalf("applied migrations = %d, want 3", migrationCount)
		}

		handler, err := buildWebHandler(cfg, repository, deps.logger, "../../static")
		if err != nil {
			t.Fatalf("build production web handler: %v", err)
		}
		apiServer := httptest.NewServer(handler)
		defer apiServer.Close()

		token := signedIntegrationToken(t, privateKey, keyID, issuer, audience)
		client := apiServer.Client()
		const organization = "Composed Integration"
		const administrator = "admin@example.test"

		response := authorizedRequest(t, client, token, http.MethodPost,
			apiServer.URL+"/org?admin="+url.QueryEscape(administrator),
			"application/json", strings.NewReader(`{"org":"`+organization+`"}`))
		assertHTTPResponse(t, response, http.StatusOK, `{"Message":"Successfully created new organization"}`)

		response = authorizedRequest(t, client, token, http.MethodGet,
			apiServer.URL+"/orgs?admin="+url.QueryEscape(administrator), "", nil)
		assertHTTPResponse(t, response, http.StatusOK, `{"orgs":["Composed Integration"]}`)

		csv := "NAME,Email,Cohort,Office\n" +
			"Alice,alice@example.test,red,North\n" +
			"Bob,bob@example.test,blue,South\n" +
			"Carol,carol@example.test,red,West\n" +
			"Dave,dave@example.test,blue,East\n"
		multipartBody, contentType := memberUpload(t, csv)
		response = authorizedRequest(t, client, token, http.MethodPost,
			apiServer.URL+"/members?org="+url.QueryEscape(organization), contentType, multipartBody)
		if response.Code != http.StatusOK {
			t.Fatalf("member synchronization status = %d, body = %s", response.Code, response.Body)
		}
		var synchronized httpapi.MembersResponse
		if err := json.Unmarshal(response.Body, &synchronized); err != nil {
			t.Fatalf("decode member synchronization response: %v", err)
		}
		if len(synchronized.Members) != 4 {
			t.Fatalf("synchronized members = %#v, want four", synchronized.Members)
		}

		response = authorizedRequest(t, client, token, http.MethodPost,
			apiServer.URL+"/crossmatchtrait?org="+url.QueryEscape(organization),
			"application/json", strings.NewReader(`{"trait":"cohort"}`))
		assertHTTPResponse(t, response, http.StatusOK, `{"Message":"Successfully set the cross match trait"}`)

		scheduled := "2026-01-01 00:00:00"
		roundURL := apiServer.URL + "/round?" + url.Values{
			"org":   []string{organization},
			"round": []string{scheduled},
		}.Encode()
		response = authorizedRequest(t, client, token, http.MethodPost, roundURL, "", nil)
		assertHTTPResponse(t, response, http.StatusOK, `{"Message":"Successfully scheduled a new round"}`)

		var sent []mailer.Message
		fakeMailer, err := mailer.NewWithSender(mailer.Config{
			Domain:    cfg.MailgunDomain,
			APIKey:    cfg.MailgunAPIKey,
			SMTPLogin: cfg.MailgunSMTPLogin,
			Timeout:   time.Second,
		}, mailer.SenderFunc(func(_ context.Context, message mailer.Message) error {
			sent = append(sent, message)
			return nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		deps.clock = fixedIntegrationClock{now: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
		deps.random = pairing.RandomFunc(func(int) int { return 0 })
		deps.newMailer = func(config.Config) (pairing.Notifier, error) { return fakeMailer, nil }
		if err := runPair(ctx, cfg, repository, deps); err != nil {
			t.Fatalf("run composed pairing scheduler: %v", err)
		}

		assertDeliveredPairs(t, sent, organization)
		assertPersistedRound(t, ctx, db, repository, organization)

		response = authorizedRequest(t, client, token, http.MethodGet,
			apiServer.URL+"/pairs?org="+url.QueryEscape(organization), "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("historical pair status = %d, body = %s", response.Code, response.Body)
		}
		var history httpapi.PairsResponse
		if err := json.Unmarshal(response.Body, &history); err != nil {
			t.Fatalf("decode historical pairs: %v", err)
		}
		if len(history.RoundPairs) != 1 || len(history.RoundPairs[0]) != 2 {
			t.Fatalf("historical pairs = %#v, want one round with two pairs", history.RoundPairs)
		}
		gotPairs := []string{
			history.RoundPairs[0][0].Member1.Email + "/" + history.RoundPairs[0][0].Member2.Email,
			history.RoundPairs[0][1].Member1.Email + "/" + history.RoundPairs[0][1].Member2.Email,
		}
		sort.Strings(gotPairs)
		wantPairs := []string{"alice@example.test/carol@example.test", "bob@example.test/dave@example.test"}
		if strings.Join(gotPairs, ",") != strings.Join(wantPairs, ",") {
			t.Fatalf("historical pair identities = %v, want %v", gotPairs, wantPairs)
		}
		if jwksRequests.Load() != 1 {
			t.Fatalf("JWKS requests = %d, want one cached fetch", jwksRequests.Load())
		}
	})
}

type capturedResponse struct {
	Code int
	Body []byte
}

func authorizedRequest(t *testing.T, client *http.Client, token, method, endpoint, contentType string, body io.Reader) capturedResponse {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, endpoint, err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return capturedResponse{Code: response.StatusCode, Body: contents}
}

func assertHTTPResponse(t *testing.T, response capturedResponse, wantCode int, wantBody string) {
	t.Helper()
	if response.Code != wantCode || string(response.Body) != wantBody {
		t.Fatalf("response = %d %q, want %d %q", response.Code, response.Body, wantCode, wantBody)
	}
}

func memberUpload(t *testing.T, csv string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("members", "members.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(part, csv); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &body, writer.FormDataContentType()
}

func signedIntegrationToken(t *testing.T, key *rsa.PrivateKey, keyID, issuer, audience string) string {
	t.Helper()
	now := time.Now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": issuer,
		"aud": audience,
		"sub": "composed-integration",
		"iat": now.Add(-time.Minute).Unix(),
		"exp": now.Add(time.Hour).Unix(),
	})
	token.Header["kid"] = keyID
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func assertDeliveredPairs(t *testing.T, sent []mailer.Message, organization string) {
	t.Helper()
	if len(sent) != 2 {
		t.Fatalf("fake mail deliveries = %d, want two", len(sent))
	}
	got := make([]string, 0, len(sent))
	for _, message := range sent {
		if message.Domain != "mg.integration.test" || message.From != organization+" Mealbot <mealbot@mg.integration.test>" || message.Subject != mailer.EmailSubject {
			t.Fatalf("mail compatibility fields = %#v", message)
		}
		if !strings.Contains(message.Text, mailer.EmailIntro) || !strings.Contains(message.Text, mailer.EmailFooter) {
			t.Fatalf("mail body lost compatibility copy: %q", message.Text)
		}
		recipients := append([]string(nil), message.To...)
		sort.Strings(recipients)
		got = append(got, strings.Join(recipients, "/"))
	}
	sort.Strings(got)
	want := []string{"alice@example.test/carol@example.test", "bob@example.test/dave@example.test"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("delivered pairs = %v, want %v", got, want)
	}
}

func assertPersistedRound(t *testing.T, ctx context.Context, db *sql.DB, repository applicationStore, organization string) {
	t.Helper()
	var pairCount int
	var done bool
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pairs WHERE organization = $1 AND round = 0`, organization).Scan(&pairCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT done FROM rounds WHERE organization = $1 AND id = 0`, organization).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if pairCount != 2 || !done {
		t.Fatalf("persisted round state: pairs=%d done=%t", pairCount, done)
	}
	members, err := repository.Members(ctx, organization, true)
	if err != nil {
		t.Fatal(err)
	}
	byEmail := make(map[string]store.Member, len(members))
	for _, member := range members {
		byEmail[member.Email] = member
	}
	for first, second := range map[string]string{
		"alice@example.test": "carol@example.test",
		"carol@example.test": "alice@example.test",
		"bob@example.test":   "dave@example.test",
		"dave@example.test":  "bob@example.test",
	} {
		if got := byEmail[first].LastRoundWith[second]; got != 0 {
			t.Errorf("persisted history %s -> %s = %d, want 0", first, second, got)
		}
	}
}

type fixedIntegrationClock struct{ now time.Time }

func (clock fixedIntegrationClock) Now() time.Time { return clock.now }

func withComposedIntegrationDB(t *testing.T, fn func(context.Context, string)) {
	t.Helper()
	if os.Getenv(composedIntegrationOptIn) != "1" {
		t.Skipf("set %s=1 to run PostgreSQL integration tests", composedIntegrationOptIn)
	}
	databaseURL := os.Getenv("MEALBOT_TEST_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = os.Getenv("DATABASE_URL")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatalf("integration database must be a postgres:// or postgresql:// URL: %v", err)
	}
	databaseName := strings.ToLower(strings.Trim(parsed.Path, "/"))
	isTestName := databaseName == "test" || strings.HasPrefix(databaseName, "test_") ||
		strings.HasSuffix(databaseName, "_test") || strings.HasSuffix(databaseName, "-test")
	if !isTestName {
		t.Fatalf("refusing integration database %q: use a test database name", databaseName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	adminDB, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open integration database: %v", err)
	}
	defer adminDB.Close()
	var serverVersion int
	if err := adminDB.QueryRowContext(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&serverVersion); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	if serverVersion/10000 != 19 {
		t.Fatalf("PostgreSQL major version = %d, want 19", serverVersion/10000)
	}

	schema := fmt.Sprintf("mealbot_composed_e2e_%d_%d", time.Now().UnixNano(), composedSchemaSequence.Add(1))
	if _, err := adminDB.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %q`, schema)); err != nil {
		t.Fatalf("create isolated schema: %v", err)
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer dropCancel()
		if _, err := adminDB.ExecContext(dropCtx, fmt.Sprintf(`DROP SCHEMA %q CASCADE`, schema)); err != nil {
			t.Errorf("drop isolated schema: %v", err)
		}
	}()
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	fn(ctx, parsed.String())
}
