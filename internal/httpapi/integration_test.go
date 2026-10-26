package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/httpapi"
	"github.com/johnamadeo/mealbot/internal/logging"
	"github.com/johnamadeo/mealbot/internal/pairing"
	"github.com/johnamadeo/mealbot/internal/store"
	"github.com/johnamadeo/mealbot/internal/testutil"
)

// fakeAuth lets requests through only when they carry "Authorization: Bearer test-token".
func fakeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodOptions && r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type fakeMailer struct {
	mu   sync.Mutex
	sent [][]string
}

func (f *fakeMailer) SendGroupEmail(ctx context.Context, orgname string, toEmails []string, toNames []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, toEmails)
	return nil
}

type client struct {
	t    *testing.T
	base string
}

func (c client) do(method, path string, contentType string, body []byte) (int, string) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test-token")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(respBody)
}

func (c client) expect(method, path string, body string, wantStatus int, wantBody string) {
	c.t.Helper()
	status, got := c.do(method, path, "", []byte(body))
	if status != wantStatus || got != wantBody {
		c.t.Errorf("%s %s = %d %s, want %d %s", method, path, status, got, wantStatus, wantBody)
	}
}

func (c client) uploadMembers(org, csv string) (int, string) {
	c.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("members", "members.csv")
	if err != nil {
		c.t.Fatal(err)
	}
	part.Write([]byte(csv))
	mw.Close()
	return c.do("POST", "/members?org="+url.QueryEscape(org), mw.FormDataContentType(), buf.Bytes())
}

func decode(t *testing.T, body string, into any) {
	t.Helper()
	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

const initialCSV = "Name,Email,College,Year\n" +
	"Ann A, ann@x.com,SM,2019\n" +
	"Bob B,bob@x.com,SM,2020\n" +
	"Cy C,cy@x.com,JE,2019\n" +
	"Dee D,dee@x.com,JE,2020\n"

// TestMembersOrgRoundPairFlow drives the whole API against a real Postgres.
func TestMembersOrgRoundPairFlow(t *testing.T) {
	db := testutil.NewDB(t)
	api := httpapi.NewServer(db, config.Config{StaticDir: t.TempDir()}, logging.Discard())
	ts := httptest.NewServer(api.Routes(httpapi.Middleware{
		MiddlewareHandlers: []func(http.Handler) http.Handler{fakeAuth, httpapi.CORS},
	}))
	defer ts.Close()
	c := client{t: t, base: ts.URL}

	// --- authentication and health
	if status, _ := (client{t: t, base: ts.URL}).do("GET", "/healthz", "", nil); status != 200 {
		t.Errorf("healthz status = %d", status)
	}
	resp, err := http.Get(ts.URL + "/orgs?admin=admin@x.com")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d", resp.StatusCode)
	}

	// --- organizations
	c.expect("POST", "/org?admin=admin@x.com", `{"org":"ysc"}`, 200, `{"Message":"Successfully created new organization"}`)
	if status, _ := c.do("POST", "/org?admin=admin@x.com", "", []byte(`{"org":"ysc"}`)); status != 500 {
		t.Errorf("duplicate org status = %d", status)
	}
	c.expect("POST", "/org?admin=admin@x.com", `{"org":""}`, 500, `{"Message":"Organization name cannot be an empty string"}`)
	c.expect("GET", "/orgs?admin=admin@x.com", "", 200, `{"orgs":["ysc"]}`)
	c.expect("GET", "/orgs?admin=nobody@x.com", "", 200, `{"orgs":[]}`)

	// --- members
	c.expect("GET", "/members?org=ysc", "", 200, `{"members":[],"traits":[],"crossMatchTrait":""}`)

	status, body := c.uploadMembers("ysc", initialCSV)
	if status != 200 {
		t.Fatalf("upload members = %d %s", status, body)
	}
	var created httpapi.CreateMembersResponse
	decode(t, body, &created)
	if len(created.Members) != 4 || strings.Join(created.Traits, ",") != "college,year" {
		t.Errorf("created = %+v", created)
	}
	if created.Members[0]["email"] != "ann@x.com" || created.Members[0]["college"] != "SM" {
		t.Errorf("first created member = %v", created.Members[0])
	}

	_, body = c.do("GET", "/members?org=ysc", "", nil)
	var listed httpapi.GetMembersResponse
	decode(t, body, &listed)
	if len(listed.Members) != 4 || listed.Members[0]["name"] != "Ann A" || listed.Members[3]["name"] != "Dee D" ||
		strings.Join(listed.Traits, ",") != "college,year" || listed.CrossMatchTrait != "" {
		t.Errorf("listed = %+v", listed)
	}

	c.expect("POST", "/crossmatchtrait?org=ysc", `{"trait":"college"}`, 200, `{"Message":"Successfully set the cross match trait"}`)
	_, body = c.do("GET", "/members?org=ysc", "", nil)
	decode(t, body, &listed)
	if listed.CrossMatchTrait != "college" {
		t.Errorf("cross match trait = %q", listed.CrossMatchTrait)
	}

	// --- rounds
	c.expect("GET", "/rounds?org=ysc", "", 200, `{"rounds":[]}`)
	for _, date := range []string{"2019-01-02 10:55:00", "2019-01-03 09:00:00"} {
		c.expect("POST", "/round?org=ysc&round="+url.QueryEscape(date), "", 200, `{"Message":"Successfully scheduled a new round"}`)
	}
	if status, _ := c.do("POST", "/round?org=ysc&round=garbage", "", nil); status != 400 {
		t.Errorf("invalid round date status = %d, want 400", status)
	}
	_, body = c.do("GET", "/rounds?org=ysc", "", nil)
	var rounds httpapi.GetRoundsResponse
	decode(t, body, &rounds)
	if len(rounds.Rounds) != 2 || !strings.HasPrefix(rounds.Rounds[0], "2019-01-02 10:55:00") {
		t.Errorf("rounds = %+v", rounds)
	}

	c.expect("POST", "/round?org=ysc&roundId=1&round="+url.QueryEscape("2019-02-03 04:05:06"), "", 200,
		`{"Message":"Succesfully changed date of the round"}`)
	_, body = c.do("GET", "/rounds?org=ysc", "", nil)
	decode(t, body, &rounds)
	if !strings.HasPrefix(rounds.Rounds[1], "2019-02-03 04:05:06") {
		t.Errorf("rounds after reschedule = %+v", rounds)
	}

	// --- pairs
	c.expect("GET", "/pairs?org=ysc", "", 200, `{"roundPairs":[]}`)

	mailer := &fakeMailer{}
	runner := pairing.NewRunner(store.New(db), mailer, logging.Discard())
	rng := rand.New(rand.NewPCG(1, 2))
	if err := runner.RunScheduler(context.Background(), true, rng); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 4 {
		t.Errorf("expected 2 emails for each of the 2 rounds, got %d", len(mailer.sent))
	}

	_, body = c.do("GET", "/pairs?org=ysc", "", nil)
	var pairs httpapi.GetPairsResponse
	decode(t, body, &pairs)
	if len(pairs.RoundPairs) != 2 || len(pairs.RoundPairs[0]) != 2 || len(pairs.RoundPairs[1]) != 2 {
		t.Fatalf("pairs = %s", body)
	}
	// existing behavior: members with an EQUAL cross match trait are preferred in round 0
	first := pairs.RoundPairs[0]
	if first[0].Member1.Email != "ann@x.com" || first[0].Member2.Email != "bob@x.com" ||
		first[1].Member1.Email != "cy@x.com" || first[1].Member2.Email != "dee@x.com" {
		t.Errorf("round 0 pairs = %+v", first)
	}
	if first[0].Member1.Name != "Ann A" {
		t.Errorf("member name = %q", first[0].Member1.Name)
	}
	// the unused third member serializes as an empty member
	if !strings.Contains(body, `"extraMember":{"Organization":"","email":"","name":"","Metadata":null,"LastRoundWith":null}`) {
		t.Errorf("unexpected extraMember encoding in %s", body)
	}
	// round 1 avoids repeating round 0
	for _, p := range pairs.RoundPairs[1] {
		if (p.Member1.Email == "ann@x.com" && p.Member2.Email == "bob@x.com") ||
			(p.Member1.Email == "cy@x.com" && p.Member2.Email == "dee@x.com") {
			t.Errorf("round 1 repeated a round 0 pair: %+v", p)
		}
	}

	// a done round is not run again
	if err := runner.RunScheduler(context.Background(), true, rng); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 4 {
		t.Errorf("done rounds must not be re-run, emails = %d", len(mailer.sent))
	}

	// --- removing a round shifts the later ones
	for _, date := range []string{"2030-01-01 00:00:00", "2030-01-02 00:00:00"} {
		c.expect("POST", "/round?org=ysc&round="+url.QueryEscape(date), "", 200, `{"Message":"Successfully scheduled a new round"}`)
	}
	c.expect("DELETE", "/round?org=ysc&roundId=2", "", 200, `{"Message":"Successfully removed round"}`)
	_, body = c.do("GET", "/rounds?org=ysc", "", nil)
	decode(t, body, &rounds)
	if len(rounds.Rounds) != 3 || !strings.HasPrefix(rounds.Rounds[2], "2030-01-02 00:00:00") {
		t.Errorf("rounds after delete = %+v", rounds)
	}

	// --- re-uploading members deactivates the ones left out
	status, body = c.uploadMembers("ysc", "Name,Email,College,Year\nAnn A,ann@x.com,SM,2019\nBob B,bob@x.com,SM,2020\nEve E,eve@x.com,JE,2021\n")
	if status != 200 {
		t.Fatalf("second upload = %d %s", status, body)
	}
	_, body = c.do("GET", "/members?org=ysc", "", nil)
	decode(t, body, &listed)
	if len(listed.Members) != 3 || listed.Members[2]["name"] != "Eve E" {
		t.Errorf("members after re-upload = %+v", listed.Members)
	}
	all, err := store.New(db).GetMembers(context.Background(), "ysc", false)
	if err != nil || len(all) != 5 {
		t.Fatalf("inactive members must be kept: %v, %d", err, len(all))
	}
	ann := all[0]
	if ann.Email != "ann@x.com" || ann.LastRoundWith["bob@x.com"] != 0 || ann.LastRoundWith["eve@x.com"] != -1 {
		t.Errorf("ann history = %v", ann.LastRoundWith)
	}

	// inactive members come out of /pairs as empty members
	_, body = c.do("GET", "/pairs?org=ysc", "", nil)
	decode(t, body, &pairs)
	if len(pairs.RoundPairs) != 2 {
		t.Errorf("pairs after re-upload = %s", body)
	}
}
