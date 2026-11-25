package mealbot

// Future lifecycle verification: the repository test lifecycle must execute
// `go test ./...`, which runs this compatibility suite without network calls.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type responseExpectation struct {
	status      int
	body        string
	contentType string
}

func assertHTTPResponse(t *testing.T, handler http.Handler, method, target string, body *bytes.Buffer, headers map[string]string, want responseExpectation) {
	t.Helper()

	var requestBody *bytes.Buffer
	if body == nil {
		requestBody = &bytes.Buffer{}
	} else {
		requestBody = body
	}
	req := httptest.NewRequest(method, target, requestBody)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)

	if recorder.Code != want.status {
		t.Fatalf("status = %d, want %d; body = %q", recorder.Code, want.status, recorder.Body.String())
	}
	if recorder.Body.String() != want.body {
		t.Fatalf("body = %q, want %q", recorder.Body.String(), want.body)
	}
	if got := recorder.Result().Header.Get("Content-Type"); got != want.contentType {
		t.Fatalf("Content-Type = %q, want %q", got, want.contentType)
	}
}

func fakeRouter() http.Handler {
	return newServeMux(Middleware{MiddlewareHandlers: []func(http.Handler) http.Handler{
		GetFakeAuthHandler,
		GetCorsHandler,
	}})
}

func TestLegacyRouteMethodContracts(t *testing.T) {
	originalGetMembers := getActiveMembersForHandler
	originalGetCrossMatch := getCrossMatchTraitForHandler
	originalGetOrganizations := getOrganizationsForHandler
	originalCreateOrganization := createOrganizationForHandler
	originalSetCrossMatch := setCrossMatchTraitForHandler
	originalGetRounds := getRoundsForHandler
	originalAddRound := addRoundForHandler
	originalRemoveRound := removeRoundForHandler
	originalRescheduleRound := rescheduleRoundForHandler
	originalGetPairs := getPairsForHandler
	defer func() {
		getActiveMembersForHandler = originalGetMembers
		getCrossMatchTraitForHandler = originalGetCrossMatch
		getOrganizationsForHandler = originalGetOrganizations
		createOrganizationForHandler = originalCreateOrganization
		setCrossMatchTraitForHandler = originalSetCrossMatch
		getRoundsForHandler = originalGetRounds
		addRoundForHandler = originalAddRound
		removeRoundForHandler = originalRemoveRound
		rescheduleRoundForHandler = originalRescheduleRound
		getPairsForHandler = originalGetPairs
	}()

	getActiveMembersForHandler = func(string) ([]MemberResponse, error) {
		return []MemberResponse{{"name": "Alice", "email": "alice@example.com", "cohort": "2024"}}, nil
	}
	getCrossMatchTraitForHandler = func(string) (string, error) { return "cohort", nil }
	getOrganizationsForHandler = func(string) ([]string, error) { return []string{"alpha", "beta"}, nil }
	createOrganizationForHandler = func(string, string) error { return nil }
	setCrossMatchTraitForHandler = func(string, string) error { return nil }
	getRoundsForHandler = func(string) ([]string, error) { return []string{"2020-01-02 03:04:05Z"}, nil }
	addRoundForHandler = func(string, string) error { return nil }
	removeRoundForHandler = func(string, int) error { return nil }
	rescheduleRoundForHandler = func(string, string, int) error { return nil }
	getPairsForHandler = func(string) ([][]GetPairsResponsePair, error) { return [][]GetPairsResponsePair{}, nil }

	router := fakeRouter()
	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   responseExpectation
	}{
		{
			name:   "get members",
			method: "GET", target: "/members?org=alpha",
			want: responseExpectation{http.StatusOK, `{"members":[{"cohort":"2024","email":"alice@example.com","name":"Alice"}],"traits":["cohort"],"crossMatchTrait":"cohort"}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "members silently ignores unsupported method",
			method: "DELETE", target: "/members?org=alpha",
			want: responseExpectation{http.StatusOK, "", ""},
		},
		{
			name:   "list organizations",
			method: "GET", target: "/orgs?admin=owner@example.com",
			want: responseExpectation{http.StatusOK, `{"orgs":["alpha","beta"]}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "create organization returns implicit 200",
			method: "POST", target: "/org?admin=owner@example.com", body: `{"org":"alpha"}`,
			want: responseExpectation{http.StatusOK, `{"Message":"Successfully created new organization"}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "set trait returns implicit 200",
			method: "POST", target: "/crossmatchtrait?org=alpha", body: `{"trait":"cohort"}`,
			want: responseExpectation{http.StatusOK, `{"Message":"Successfully set the cross match trait"}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "get rounds",
			method: "GET", target: "/rounds?org=alpha",
			want: responseExpectation{http.StatusOK, `{"rounds":["2020-01-02 03:04:05Z"]}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "add round returns implicit 200",
			method: "POST", target: "/round?org=alpha&round=2020-01-02T03%3A04%3A05Z",
			want: responseExpectation{http.StatusOK, `{"Message":"Successfully scheduled a new round"}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "reschedule round returns implicit 200 and typo",
			method: "POST", target: "/round?org=alpha&round=2020-01-02T03%3A04%3A05Z&roundId=2",
			want: responseExpectation{http.StatusOK, `{"Message":"Succesfully changed date of the round"}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "remove round returns implicit 200",
			method: "DELETE", target: "/round?org=alpha&roundId=2",
			want: responseExpectation{http.StatusOK, `{"Message":"Successfully removed round"}`, "text/plain; charset=utf-8"},
		},
		{
			name:   "get empty pair history",
			method: "GET", target: "/pairs?org=alpha",
			want: responseExpectation{http.StatusOK, `{"roundPairs":[]}`, ""},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertHTTPResponse(t, router, test.method, test.target, bytes.NewBufferString(test.body), nil, test.want)
		})
	}
}

func TestLegacyMethodRejections(t *testing.T) {
	router := fakeRouter()
	tests := []struct {
		method string
		target string
		body   string
	}{
		{"POST", "/orgs?admin=owner@example.com", "Only GET requests are allowed at this route"},
		{"GET", "/org?admin=owner@example.com", "Only POST requests are allowed at this route"},
		{"GET", "/crossmatchtrait?org=alpha", "Only POST requests are allowed at this route"},
		{"POST", "/rounds?org=alpha", "Only GET requests are allowed at this route"},
		{"GET", "/round?org=alpha", "Only POST and DELETE requests are allowed at this route"},
		{"POST", "/pairs?org=alpha", "Only GET requests are allowed at this route"},
	}
	for _, test := range tests {
		t.Run(test.method+" "+test.target, func(t *testing.T) {
			assertHTTPResponse(t, router, test.method, test.target, nil, nil, responseExpectation{
				http.StatusMethodNotAllowed,
				`{"Message":"` + test.body + `"}`,
				"",
			})
		})
	}
}

func TestLegacyQueryParameterContracts(t *testing.T) {
	tests := []struct {
		name string
		url  string
		key  string
		want string
	}{
		{"missing single", "/members", "org", "Request query parameters must contain org"},
		{"duplicate single", "/members?org=one&org=two", "org", "Request query parameters must contain org"},
		{"empty single is accepted", "/members?org=", "org", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", test.url, nil)
			got, err := getQueryParam(req, test.key)
			if test.want == "" {
				if err != nil || got != "" {
					t.Fatalf("got value %q, error %v; want accepted empty value", got, err)
				}
				return
			}
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}

	req := httptest.NewRequest("POST", "/round?org=one&round=now&round=again", nil)
	_, err := getQueryParams(req, []string{"org", "round"})
	if err == nil || err.Error() != "Request query parameters does not contain round" {
		t.Fatalf("duplicate multi-value error = %v", err)
	}

	originalAddRound := addRoundForHandler
	defer func() { addRoundForHandler = originalAddRound }()
	called := false
	addRoundForHandler = func(string, string) error { called = true; return nil }
	assertHTTPResponse(t, http.HandlerFunc(RoundHandler), "POST", "/round?org=one&round=now&roundId=1&roundId=2", nil, nil, responseExpectation{
		http.StatusOK,
		`{"Message":"Successfully scheduled a new round"}`,
		"text/plain; charset=utf-8",
	})
	if !called {
		t.Fatal("duplicate roundId should route to AddRoundHandler")
	}
}

func TestQueryFailuresReachClientsWithExactLegacyText(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
		method  string
		target  string
		body    string
		want    string
	}{
		{"members missing org", http.HandlerFunc(GetMembersHandler), "GET", "/members", "", "Request query parameters must contain org"},
		{"members duplicate org", http.HandlerFunc(GetMembersHandler), "GET", "/members?org=one&org=two", "", "Request query parameters must contain org"},
		{"orgs missing admin", http.HandlerFunc(GetOrganizationsHandler), "GET", "/orgs", "", "request query parameters must contain 'admin'"},
		{"orgs duplicate admin", http.HandlerFunc(GetOrganizationsHandler), "GET", "/orgs?admin=one&admin=two", "", "request query parameters must contain 'admin'"},
		{"create org missing admin", http.HandlerFunc(CreateOrganizationHandler), "POST", "/org", `{"org":"alpha"}`, "request query parameters must contain 'admin'"},
		{"trait missing org", http.HandlerFunc(CrossMatchTraitHandler), "POST", "/crossmatchtrait", `{"trait":"cohort"}`, "Request query parameters must contain org"},
		{"rounds duplicate org", http.HandlerFunc(GetRoundsHandler), "GET", "/rounds?org=one&org=two", "", "Request query parameters must contain org"},
		{"add round missing round", http.HandlerFunc(RoundHandler), "POST", "/round?org=alpha", "", "Request query parameters does not contain round"},
		{"remove round missing id", http.HandlerFunc(RoundHandler), "DELETE", "/round?org=alpha", "", "Request query parameters does not contain roundId"},
		{"pairs missing org", http.HandlerFunc(GetPairsHandler), "GET", "/pairs", "", "Request query parameters must contain org"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertHTTPResponse(t, test.handler, test.method, test.target, bytes.NewBufferString(test.body), nil, responseExpectation{
				http.StatusBadRequest,
				`{"Message":"` + test.want + `"}`,
				"",
			})
		})
	}
}

func TestMalformedJSONAndMultipartContracts(t *testing.T) {
	assertHTTPResponse(t, http.HandlerFunc(CreateOrganizationHandler), "POST", "/org?admin=owner", bytes.NewBufferString(`{"org":`), nil, responseExpectation{
		http.StatusBadRequest,
		`{"Message":"unexpected end of JSON input"}`,
		"",
	})
	assertHTTPResponse(t, http.HandlerFunc(CrossMatchTraitHandler), "POST", "/crossmatchtrait?org=alpha", bytes.NewBufferString(`{"trait":`), nil, responseExpectation{
		http.StatusBadRequest,
		`{"Message":"Request body is malformed"}`,
		"",
	})
	assertHTTPResponse(t, http.HandlerFunc(CreateMembersHandler), "POST", "/members?org=alpha", bytes.NewBufferString("not multipart"), map[string]string{
		"Content-Type": "multipart/form-data",
	}, responseExpectation{
		http.StatusBadRequest,
		`{"Message":"no multipart boundary param in Content-Type"}`,
		"",
	})

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	if err := writer.WriteField("wrong-field", "value"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	assertHTTPResponse(t, http.HandlerFunc(CreateMembersHandler), "POST", "/members?org=alpha", &multipartBody, map[string]string{
		"Content-Type": writer.FormDataContentType(),
	}, responseExpectation{
		http.StatusBadRequest,
		`{"Message":"http: no such file"}`,
		"",
	})
}

func TestCreateMembersMultipartResponseContract(t *testing.T) {
	originalCreateMembers := createMembersFromCSVForHandler
	defer func() { createMembersFromCSVForHandler = originalCreateMembers }()
	createMembersFromCSVForHandler = func(orgname, filename string) ([]MemberResponse, error) {
		if orgname != "alpha" || !strings.HasSuffix(filename, "compatibility-upload.csv") {
			return nil, errors.New("unexpected upload arguments")
		}
		return []MemberResponse{{"name": "Alice", "email": "alice@example.com", "cohort": "2024"}}, nil
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("members", "compatibility-upload.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte("name,email,cohort\nAlice,alice@example.com,2024\n")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	defer os.Remove("./csv/compatibility-upload.csv")

	assertHTTPResponse(t, http.HandlerFunc(CreateMembersHandler), "POST", "/members?org=alpha", &body, map[string]string{
		"Content-Type": writer.FormDataContentType(),
	}, responseExpectation{
		http.StatusOK,
		`{"members":[{"cohort":"2024","email":"alice@example.com","name":"Alice"}],"traits":["cohort"]}`,
		"text/plain; charset=utf-8",
	})
}

func TestHandlerDatabaseErrorsUseLegacyMessageEnvelope(t *testing.T) {
	dbErr := errors.New("database unavailable")
	tests := []struct {
		name    string
		handler http.Handler
		method  string
		target  string
		install func() func()
	}{
		{
			"members", http.HandlerFunc(GetMembersHandler), "GET", "/members?org=alpha",
			func() func() {
				old := getActiveMembersForHandler
				getActiveMembersForHandler = func(string) ([]MemberResponse, error) { return nil, dbErr }
				return func() { getActiveMembersForHandler = old }
			},
		},
		{
			"organizations", http.HandlerFunc(GetOrganizationsHandler), "GET", "/orgs?admin=owner",
			func() func() {
				old := getOrganizationsForHandler
				getOrganizationsForHandler = func(string) ([]string, error) { return nil, dbErr }
				return func() { getOrganizationsForHandler = old }
			},
		},
		{
			"create organization", http.HandlerFunc(CreateOrganizationHandler), "POST", "/org?admin=owner",
			func() func() {
				old := createOrganizationForHandler
				createOrganizationForHandler = func(string, string) error { return dbErr }
				return func() { createOrganizationForHandler = old }
			},
		},
		{
			"cross match", http.HandlerFunc(CrossMatchTraitHandler), "POST", "/crossmatchtrait?org=alpha",
			func() func() {
				old := setCrossMatchTraitForHandler
				setCrossMatchTraitForHandler = func(string, string) error { return dbErr }
				return func() { setCrossMatchTraitForHandler = old }
			},
		},
		{
			"rounds", http.HandlerFunc(GetRoundsHandler), "GET", "/rounds?org=alpha",
			func() func() {
				old := getRoundsForHandler
				getRoundsForHandler = func(string) ([]string, error) { return nil, dbErr }
				return func() { getRoundsForHandler = old }
			},
		},
		{
			"pairs", http.HandlerFunc(GetPairsHandler), "GET", "/pairs?org=alpha",
			func() func() {
				old := getPairsForHandler
				getPairsForHandler = func(string) ([][]GetPairsResponsePair, error) { return nil, dbErr }
				return func() { getPairsForHandler = old }
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			restore := test.install()
			defer restore()
			body := bytes.NewBufferString(`{"org":"alpha","trait":"cohort"}`)
			assertHTTPResponse(t, test.handler, test.method, test.target, body, nil, responseExpectation{
				http.StatusInternalServerError,
				`{"Message":"database unavailable"}`,
				"",
			})
		})
	}
}

func TestMembersCrossMatchLookupDatabaseError(t *testing.T) {
	originalMembers := getActiveMembersForHandler
	originalTrait := getCrossMatchTraitForHandler
	defer func() {
		getActiveMembersForHandler = originalMembers
		getCrossMatchTraitForHandler = originalTrait
	}()
	getActiveMembersForHandler = func(string) ([]MemberResponse, error) { return []MemberResponse{}, nil }
	getCrossMatchTraitForHandler = func(string) (string, error) { return "", errors.New("database unavailable") }
	assertHTTPResponse(t, http.HandlerFunc(GetMembersHandler), "GET", "/members?org=alpha", nil, nil, responseExpectation{
		http.StatusInternalServerError,
		`{"Message":"database unavailable"}`,
		"",
	})
}

func TestRoundMutationDatabaseErrorStatuses(t *testing.T) {
	dbErr := errors.New("database unavailable")
	originalAdd := addRoundForHandler
	originalRemove := removeRoundForHandler
	originalReschedule := rescheduleRoundForHandler
	defer func() {
		addRoundForHandler = originalAdd
		removeRoundForHandler = originalRemove
		rescheduleRoundForHandler = originalReschedule
	}()
	addRoundForHandler = func(string, string) error { return dbErr }
	removeRoundForHandler = func(string, int) error { return dbErr }
	rescheduleRoundForHandler = func(string, string, int) error { return dbErr }

	tests := []struct {
		method string
		target string
		status int
	}{
		{"POST", "/round?org=alpha&round=now", http.StatusBadRequest},
		{"POST", "/round?org=alpha&round=now&roundId=1", http.StatusInternalServerError},
		{"DELETE", "/round?org=alpha&roundId=1", http.StatusInternalServerError},
	}
	for _, test := range tests {
		assertHTTPResponse(t, http.HandlerFunc(RoundHandler), test.method, test.target, nil, nil, responseExpectation{
			test.status,
			`{"Message":"database unavailable"}`,
			"",
		})
	}
}

func TestOptionsCorsAndAuthenticationContracts(t *testing.T) {
	called := false
	core := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := GetCorsHandler(GetAuthHandler(core))
	assertHTTPResponse(t, handler, "OPTIONS", "/members", nil, map[string]string{"Origin": "https://client.example"}, responseExpectation{
		http.StatusOK,
		"",
		"",
	})
	if called {
		t.Fatal("OPTIONS should stop in CORS middleware before auth and the handler")
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/members", nil)
	req.Header.Set("Origin", "https://client.example")
	handler.ServeHTTP(recorder, req)
	wantHeaders := map[string]string{
		"Access-Control-Allow-Origin":  "https://client.example",
		"Access-Control-Allow-Headers": "Authorization, Content-Type, Origin, Accept, token",
		"Access-Control-Allow-Methods": "GET, POST, DELETE",
	}
	for key, want := range wantHeaders {
		if got := recorder.Header().Get(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}

	authCoreCalled := false
	auth := &CustomJWTMiddleware{}
	authHandler := auth.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { authCoreCalled = true }))
	for _, authHeader := range []string{"", "Basic token", "Bearer", "Bearer not-a-token"} {
		recorder = httptest.NewRecorder()
		req = httptest.NewRequest("GET", "/members", nil)
		req.Header.Set("Authorization", authHeader)
		authHandler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
			t.Fatalf("JWT failure for %q = status %d body %q; want empty 200", authHeader, recorder.Code, recorder.Body.String())
		}
		if len(recorder.Header()) != 0 {
			t.Fatalf("JWT failure for %q invented headers: %v", authHeader, recorder.Header())
		}
	}
	if authCoreCalled {
		t.Fatal("JWT failures must not call the protected handler")
	}
}

func TestEveryAPIRouteAllowsUnauthenticatedOptions(t *testing.T) {
	router := fakeRouter()
	for _, path := range []string{"/members", "/orgs", "/org", "/crossmatchtrait", "/rounds", "/round", "/pairs"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest("OPTIONS", path, nil)
			req.Header.Set("Origin", "https://client.example")
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
				t.Fatalf("OPTIONS = status %d body %q", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://client.example" {
				t.Fatalf("allow origin = %q", got)
			}
		})
	}
}

func TestRegisteredAPIRouteRejectsMissingJWTBeforeDatabase(t *testing.T) {
	router := newServeMux(Middleware{MiddlewareHandlers: []func(http.Handler) http.Handler{
		GetAuthHandler,
		GetCorsHandler,
	}})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/members?org=alpha", nil)
	req.Header.Set("Origin", "https://client.example")
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK || recorder.Body.Len() != 0 {
		t.Fatalf("missing JWT = status %d body %q; want legacy empty 200", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://client.example" {
		t.Fatalf("allow origin = %q", got)
	}
}

func TestMiddlewareApplyOrder(t *testing.T) {
	events := []string{}
	first := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			events = append(events, "first-before")
			next.ServeHTTP(w, r)
			events = append(events, "first-after")
		})
	}
	second := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			events = append(events, "second-before")
			next.ServeHTTP(w, r)
			events = append(events, "second-after")
		})
	}
	mw := Middleware{MiddlewareHandlers: []func(http.Handler) http.Handler{first, second}}
	handler := mw.Apply(func(http.ResponseWriter, *http.Request) { events = append(events, "core") })
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"second-before", "first-before", "core", "first-after", "second-after"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("middleware order = %v, want %v", events, want)
	}
}

func TestStaticFilesRemainPublicAndExact(t *testing.T) {
	router := fakeRouter()
	files := []struct {
		path        string
		contentType string
		sha256      string
	}{
		{"privacy.html", "text/html; charset=utf-8", "8002d3f7aa546aac4f582314d8c088d53649c0b6164f2c5b993dc222c315a17c"},
		{"sample.csv", "text/csv; charset=utf-8", "bc8652405a29f77ae729aa6da49f2603326b1d94d2bed60117c99cfcb32e9cd1"},
	}
	for _, file := range files {
		t.Run(file.path, func(t *testing.T) {
			wantBody, err := ioutil.ReadFile("./static/" + file.path)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(wantBody)
			if got := hex.EncodeToString(digest[:]); got != file.sha256 {
				t.Fatalf("tracked %s digest = %s, want %s", file.path, got, file.sha256)
			}
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/"+file.path, nil)
			router.ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if !bytes.Equal(recorder.Body.Bytes(), wantBody) {
				t.Fatalf("served bytes for %s differ from tracked asset", file.path)
			}
			if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("static files must bypass API CORS/auth middleware")
			}
			if got := recorder.Header().Get("Content-Type"); got != file.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, file.contentType)
			}
			if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(wantBody)) {
				t.Fatalf("Content-Length = %q, want %d", got, len(wantBody))
			}
		})
	}
}

func TestPairsResponsePreservesExtraMemberRepresentation(t *testing.T) {
	original := getPairsForHandler
	defer func() { getPairsForHandler = original }()
	getPairsForHandler = func(string) ([][]GetPairsResponsePair, error) {
		return [][]GetPairsResponsePair{{
			{
				Member1: Member{Email: "a@example.com", Name: "Alice"},
				Member2: Member{Email: "b@example.com", Name: "Bob"},
			},
			{
				Member1:     Member{Email: "c@example.com", Name: "Carol"},
				Member2:     Member{Email: "d@example.com", Name: "Dan"},
				ExtraMember: Member{Email: "e@example.com", Name: "Eve"},
			},
		}}, nil
	}
	want := `{"roundPairs":[[{"member1":{"Organization":"","email":"a@example.com","name":"Alice","Metadata":null,"LastRoundWith":null},"member2":{"Organization":"","email":"b@example.com","name":"Bob","Metadata":null,"LastRoundWith":null},"extraMember":{"Organization":"","email":"","name":"","Metadata":null,"LastRoundWith":null}},{"member1":{"Organization":"","email":"c@example.com","name":"Carol","Metadata":null,"LastRoundWith":null},"member2":{"Organization":"","email":"d@example.com","name":"Dan","Metadata":null,"LastRoundWith":null},"extraMember":{"Organization":"","email":"e@example.com","name":"Eve","Metadata":null,"LastRoundWith":null}}]]}`
	assertHTTPResponse(t, http.HandlerFunc(GetPairsHandler), "GET", "/pairs?org=alpha", nil, nil, responseExpectation{
		http.StatusOK, want, "",
	})
}

func TestLegacyCommandDispatch(t *testing.T) {
	originalPair := runPairingSchedulerForCommand
	originalMigrate := migrateForCommand
	defer func() {
		runPairingSchedulerForCommand = originalPair
		migrateForCommand = originalMigrate
	}()

	pairCalls := 0
	migrateCalls := 0
	runPairingSchedulerForCommand = func(testMode bool) error {
		pairCalls++
		if testMode {
			t.Fatal("pair command must use production mode")
		}
		return errors.New("pair failed")
	}
	migrateForCommand = func() error { migrateCalls++; return nil }

	var out bytes.Buffer
	if !dispatchCommand([]string{"mealbot", "pair"}, &out) || out.String() != "pair failed\n" || pairCalls != 1 {
		t.Fatalf("pair dispatch: handled=%v output=%q calls=%d", true, out.String(), pairCalls)
	}
	out.Reset()
	if !dispatchCommand([]string{"mealbot", "migrate"}, &out) || out.Len() != 0 || migrateCalls != 1 {
		t.Fatalf("migrate dispatch: output=%q calls=%d", out.String(), migrateCalls)
	}
	out.Reset()
	if !dispatchCommand([]string{"mealbot", "unknown"}, &out) || out.String() != "argument 'unknown' not recognized" {
		t.Fatalf("unknown dispatch output = %q", out.String())
	}
	if dispatchCommand([]string{"mealbot"}, &out) {
		t.Fatal("zero-argument invocation must fall through to web serving")
	}
}
