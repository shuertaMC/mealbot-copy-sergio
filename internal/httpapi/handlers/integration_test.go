package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/testutil/pgtest"
)

func TestHandlersAgainstPostgres(t *testing.T) {
	pool := pgtest.NewPool(t)
	h := New(pool, &mail.Fake{}, config.Config{CSVDir: t.TempDir()})

	// organization
	rec := do(h.CreateOrganization, "POST", "/org?admin=admin@x.com", `{"org":"ysc"}`)
	if rec.Code != http.StatusOK || message(t, rec) != "Successfully created new organization" {
		t.Fatalf("create org: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h.CreateOrganization, "POST", "/org?admin=admin@x.com", `{"org":"ysc"}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "duplicate key") {
		t.Fatalf("duplicate org: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h.GetOrganizations, "GET", "/orgs?admin=admin@x.com", "")
	if rec.Body.String() != `{"orgs":["ysc"]}` {
		t.Fatalf("orgs: %s", rec.Body.String())
	}

	// cross match trait
	rec = do(h.CrossMatchTrait, "POST", "/crossmatchtrait?org=ysc", `{"trait":"year"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("trait: %d %s", rec.Code, rec.Body.String())
	}

	// members through a multipart CSV upload
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("members", "members.csv")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("name,email,year\nAlice,a@x.com,2019\nBob,b@x.com,2020\n"))
	mw.Close()
	req := httptest.NewRequest("POST", "/members?org=ysc", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	h.Members(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(h.Members, "GET", "/members?org=ysc", "")
	var members GetMembersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &members); err != nil {
		t.Fatal(err)
	}
	if len(members.Members) != 2 || members.CrossMatchTrait != "year" ||
		len(members.Traits) != 1 || members.Traits[0] != "year" ||
		members.Members[0]["name"] != "Alice" || members.Members[0]["year"] != "2019" {
		t.Fatalf("members: %+v", members)
	}

	// rounds
	rec = do(h.Round, "POST", "/round?org=ysc&round=2019-01-02%2010:55:00", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("add round: %d %s", rec.Code, rec.Body.String())
	}
	do(h.Round, "POST", "/round?org=ysc&round=2019-02-02%2010:55:00", "")
	rec = do(h.GetRounds, "GET", "/rounds?org=ysc", "")
	var rounds GetRoundsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &rounds); err != nil {
		t.Fatal(err)
	}
	if len(rounds.Rounds) != 2 || !strings.HasPrefix(rounds.Rounds[0], "2019-01-02 10:55:00") {
		t.Fatalf("rounds: %+v", rounds)
	}

	rec = do(h.Round, "POST", "/round?org=ysc&roundId=1&round=2019-03-03%2011:00:00", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reschedule: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h.Round, "DELETE", "/round?org=ysc&roundId=0", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("remove: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h.GetRounds, "GET", "/rounds?org=ysc", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &rounds); err != nil {
		t.Fatal(err)
	}
	if len(rounds.Rounds) != 1 || !strings.HasPrefix(rounds.Rounds[0], "2019-03-03 11:00:00") {
		t.Fatalf("rounds after remove: %+v", rounds)
	}

	// pairs (none yet)
	rec = do(h.GetPairs, "GET", "/pairs?org=ysc", "")
	if rec.Body.String() != `{"roundPairs":[]}` {
		t.Fatalf("pairs: %s", rec.Body.String())
	}
}
