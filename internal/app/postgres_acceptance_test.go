//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresVerticalAcceptance(t *testing.T) {
	if os.Getenv("RUN_POSTGRES_INTEGRATION") != "1" {
		t.Skip("set RUN_POSTGRES_INTEGRATION=1 with a disposable DATABASE_URL")
	}
	databaseURL := os.Getenv("DATABASE_URL")
	if _, err := validatedAcceptanceDatabaseName(databaseURL); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open PostgreSQL verification connection: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close PostgreSQL verification connection: %v", err)
		}
	})
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	var serverVersion string
	if err := database.QueryRowContext(ctx, "SHOW server_version_num").Scan(&serverVersion); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}
	versionNumber, err := strconv.Atoi(serverVersion)
	if err != nil || versionNumber/10000 != 17 {
		t.Fatalf("PostgreSQL server_version_num = %q, want major version 17", serverVersion)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate acceptance signing key: %v", err)
	}
	const keyID = "postgres-vertical-acceptance"
	jwks := newAcceptanceJWKSServer(t, keyID, &privateKey.PublicKey)
	defer jwks.Close()

	staticDirectory, err := filepath.Abs(filepath.Join("..", "..", "static"))
	if err != nil {
		t.Fatalf("resolve static directory: %v", err)
	}
	issuer := jwks.URL + "/"
	const audience = "mealbot-postgres-vertical"
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("DB_USER", "")
	t.Setenv("DB_NAME", "")
	t.Setenv("PORT", "8080")
	t.Setenv("STATIC_DIR", staticDirectory)
	t.Setenv("AUTH0_ISSUER", issuer)
	t.Setenv("AUTH0_AUDIENCE", audience)
	t.Setenv("AUTH0_JWKS_URL", jwks.URL+"/.well-known/jwks.json")
	t.Setenv("REQUEST_TIMEOUT", "5s")
	t.Setenv("JWKS_TIMEOUT", "5s")

	factories := ProductionFactories()
	configuration, err := factories.LoadConfig()
	if err != nil {
		t.Fatalf("load production configuration: %v", err)
	}
	repository, err := factories.OpenRepository(ctx, configuration.Database())
	if err != nil {
		t.Fatalf("open production repository: %v", err)
	}
	t.Cleanup(func() {
		if err := repository.Close(); err != nil {
			t.Errorf("close production repository: %v", err)
		}
	})
	if err := repository.Migrate(ctx); err != nil {
		t.Fatalf("apply production repository migrations: %v", err)
	}

	uniqueBytes := make([]byte, 8)
	if _, err := rand.Read(uniqueBytes); err != nil {
		t.Fatalf("generate unique acceptance identity: %v", err)
	}
	uniqueID := hex.EncodeToString(uniqueBytes)
	organization := "postgres-vertical-" + uniqueID
	admin := "postgres-vertical-" + uniqueID + "@example.test"
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := database.ExecContext(cleanupContext,
			"DELETE FROM organizations WHERE name = $1 AND admin = $2", organization, admin,
		); err != nil {
			t.Errorf("clean up acceptance organization %q: %v", organization, err)
		}
	})

	authenticate, err := factories.NewAuthenticator(configuration.Auth0(), configuration.JWKSTimeout())
	if err != nil {
		t.Fatalf("compose production authentication: %v", err)
	}
	composed, err := factories.NewServer(configuration, repository, authenticate, factories.Logger())
	if err != nil {
		t.Fatalf("compose production HTTP server: %v", err)
	}
	server, ok := composed.(*http.Server)
	if !ok {
		t.Fatalf("production server type = %T, want *http.Server", composed)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral loopback port: %v", err)
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			t.Errorf("shutdown production HTTP server: %v", err)
		}
		if err := <-serveErrors; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve production HTTP server: %v", err)
		}
	})

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer: issuer, Audience: jwt.ClaimStrings{audience},
		IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
	})
	token.Header["kid"] = keyID
	signedToken, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("sign acceptance token: %v", err)
	}
	authorization := "Bearer " + signedToken
	baseURL := "http://" + listener.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}

	createStatus, createBody := acceptanceRequest(t, client, http.MethodPost,
		baseURL+"/org?admin="+url.QueryEscape(admin), authorization,
		[]byte(fmt.Sprintf(`{"org":%q}`, organization)),
	)
	if createStatus != http.StatusCreated || !strings.Contains(string(createBody), "Successfully created new organization") {
		t.Fatalf("create organization response = %d %q", createStatus, createBody)
	}

	listStatus, listBody := acceptanceRequest(t, client, http.MethodGet,
		baseURL+"/orgs?admin="+url.QueryEscape(admin), authorization, nil,
	)
	var listed struct {
		Organizations []string `json:"orgs"`
	}
	if err := json.Unmarshal(listBody, &listed); err != nil {
		t.Fatalf("decode organization list response %q: %v", listBody, err)
	}
	if listStatus != http.StatusOK || len(listed.Organizations) != 1 || listed.Organizations[0] != organization {
		t.Fatalf("list organizations response = %d %#v", listStatus, listed.Organizations)
	}

	privacyStatus, privacyBody := acceptanceRequest(t, client, http.MethodGet,
		baseURL+"/privacy.html", "", nil,
	)
	if privacyStatus != http.StatusOK || !bytes.Contains(privacyBody, []byte("<title>Privacy Policy</title>")) {
		t.Fatalf("privacy response = %d %q", privacyStatus, privacyBody)
	}
}

func newAcceptanceJWKSServer(t *testing.T, keyID string, publicKey *rsa.PublicKey) *httptest.Server {
	t.Helper()
	document, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": keyID, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(publicKey.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
	}}})
	if err != nil {
		t.Fatalf("marshal local JWKS: %v", err)
	}
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/.well-known/jwks.json" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(document)
	}))
}

func acceptanceRequest(t *testing.T, client *http.Client, method, target, authorization string, body []byte) (int, []byte) {
	t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform %s request: %v", method, err)
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read %s response: %v; close response: %v", method, readErr, closeErr)
	}
	return response.StatusCode, responseBody
}

func validatedAcceptanceDatabaseName(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("DATABASE_URL must be a non-empty PostgreSQL URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil || (!strings.EqualFold(parsed.Scheme, "postgres") && !strings.EqualFold(parsed.Scheme, "postgresql")) {
		return "", errors.New("DATABASE_URL must be a valid PostgreSQL URL")
	}
	if parsed.Opaque != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") || len(parsed.Path) == 1 {
		return "", errors.New("DATABASE_URL must select a database in its URL path")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", fmt.Errorf("DATABASE_URL has an invalid query: %w", err)
	}
	for key := range query {
		if strings.EqualFold(key, "dbname") {
			return "", errors.New("DATABASE_URL must select its database in the URL path, not a dbname query parameter")
		}
	}
	databaseName := strings.TrimPrefix(parsed.Path, "/")
	if databaseName == "" || strings.ContainsRune(databaseName, '\x00') || !hasAcceptanceTestSegment(databaseName) {
		return "", fmt.Errorf("refusing integration test against non-test database %q", databaseName)
	}
	return databaseName, nil
}

func hasAcceptanceTestSegment(databaseName string) bool {
	segmentStart := -1
	for index, character := range databaseName {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			if segmentStart == -1 {
				segmentStart = index
			}
			continue
		}
		if segmentStart != -1 && strings.EqualFold(databaseName[segmentStart:index], "test") {
			return true
		}
		segmentStart = -1
	}
	return segmentStart != -1 && strings.EqualFold(databaseName[segmentStart:], "test")
}
