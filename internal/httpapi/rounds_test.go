package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeRoundStore struct {
	rounds          []string
	listErr         error
	addErr          error
	rescheduleErr   error
	deleteErr       error
	listedOrg       string
	addedOrg        string
	addedDate       string
	rescheduledOrg  string
	rescheduledDate string
	rescheduledID   int
	deletedOrg      string
	deletedID       int
	addCalls        int
	rescheduleCalls int
	deleteCalls     int
}

func (s *fakeRoundStore) ListRounds(_ context.Context, organization string) ([]string, error) {
	s.listedOrg = organization
	return s.rounds, s.listErr
}

func (s *fakeRoundStore) AddRound(_ context.Context, organization, scheduled string) (int, error) {
	s.addCalls++
	s.addedOrg, s.addedDate = organization, scheduled
	return 7, s.addErr
}

func (s *fakeRoundStore) RescheduleRound(_ context.Context, organization, scheduled string, roundID int) error {
	s.rescheduleCalls++
	s.rescheduledOrg, s.rescheduledDate, s.rescheduledID = organization, scheduled, roundID
	return s.rescheduleErr
}

func (s *fakeRoundStore) DeleteRound(_ context.Context, organization string, roundID int) error {
	s.deleteCalls++
	s.deletedOrg, s.deletedID = organization, roundID
	return s.deleteErr
}

func assertRoundHTTP(t *testing.T, handler http.Handler, method, target string, status int, body, contentType string) {
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

func TestRoundHandlersGetCompatibility(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		rounds     []string
		wantBody   string
		wantOrg    string
		wantStatus int
		wantType   string
	}{
		{"values", "/rounds?org=alpha", []string{"2026-01-02 03:04:05Z"}, `{"rounds":["2026-01-02 03:04:05Z"]}`, "alpha", http.StatusOK, "text/plain; charset=utf-8"},
		{"empty organization accepted", "/rounds?org=", []string{}, `{"rounds":[]}`, "", http.StatusOK, "text/plain; charset=utf-8"},
		{"nil result remains null", "/rounds?org=alpha", nil, `{"rounds":null}`, "alpha", http.StatusOK, "text/plain; charset=utf-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeRoundStore{rounds: test.rounds}
			handlers := NewRoundHandlers(store, NewResponder(nil))
			assertRoundHTTP(t, http.HandlerFunc(handlers.Get), http.MethodGet, test.target, test.wantStatus, test.wantBody, test.wantType)
			if store.listedOrg != test.wantOrg {
				t.Fatalf("listed organization = %q, want %q", store.listedOrg, test.wantOrg)
			}
		})
	}
}

func TestRoundsEndpointMethodRestriction(t *testing.T) {
	handler := http.HandlerFunc(NewRoundHandlers(&fakeRoundStore{}, NewResponder(nil)).Get)
	assertRoundHTTP(t, handler, http.MethodPost, "/rounds?org=alpha", http.StatusMethodNotAllowed, `{"Message":"Only GET requests are allowed at this route"}`, "")
}

func TestRoundsEndpointRepositoryFailure(t *testing.T) {
	t.Run("store error", func(t *testing.T) {
		handler := http.HandlerFunc(NewRoundHandlers(&fakeRoundStore{listErr: errors.New("round failure")}, NewResponder(nil)).Get)
		assertRoundHTTP(t, handler, http.MethodGet, "/rounds?org=alpha", http.StatusInternalServerError, `{"Message":"round failure"}`, "")
	})
	t.Run("missing store", func(t *testing.T) {
		handler := http.HandlerFunc(NewRoundHandlers(nil, NewResponder(nil)).Get)
		assertRoundHTTP(t, handler, http.MethodGet, "/rounds?org=alpha", http.StatusInternalServerError, `{"Message":"round store is unavailable"}`, "")
	})
}

func TestRoundHandlersGetQueryFailures(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		store  RoundStore
		status int
		body   string
	}{
		{"missing organization", http.MethodGet, "/rounds", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`},
		{"duplicate organization", http.MethodGet, "/rounds?org=one&org=two", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters must contain org"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlers := NewRoundHandlers(test.store, NewResponder(nil))
			assertRoundHTTP(t, http.HandlerFunc(handlers.Get), test.method, test.target, test.status, test.body, "")
		})
	}
}

func TestRoundHandlersDispatcherCompatibility(t *testing.T) {
	t.Run("missing roundId adds", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=alpha&round=tomorrow", http.StatusOK, `{"Message":"Successfully scheduled a new round"}`, "text/plain; charset=utf-8")
		if store.addCalls != 1 || store.addedOrg != "alpha" || store.addedDate != "tomorrow" || store.rescheduleCalls != 0 {
			t.Fatalf("add calls=%d org=%q date=%q reschedules=%d", store.addCalls, store.addedOrg, store.addedDate, store.rescheduleCalls)
		}
	})

	t.Run("duplicate roundId adds", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=alpha&round=tomorrow&roundId=1&roundId=2", http.StatusOK, `{"Message":"Successfully scheduled a new round"}`, "text/plain; charset=utf-8")
		if store.addCalls != 1 || store.rescheduleCalls != 0 {
			t.Fatalf("add calls=%d reschedule calls=%d", store.addCalls, store.rescheduleCalls)
		}
	})

	t.Run("unique roundId reschedules", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=alpha&round=later&roundId=3", http.StatusOK, `{"Message":"Succesfully changed date of the round"}`, "text/plain; charset=utf-8")
		if store.rescheduleCalls != 1 || store.rescheduledOrg != "alpha" || store.rescheduledDate != "later" || store.rescheduledID != 3 || store.addCalls != 0 {
			t.Fatalf("reschedule calls=%d values=%q/%q/%d add calls=%d", store.rescheduleCalls, store.rescheduledOrg, store.rescheduledDate, store.rescheduledID, store.addCalls)
		}
	})

	t.Run("delete removes", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodDelete, "/round?org=alpha&roundId=4", http.StatusOK, `{"Message":"Successfully removed round"}`, "text/plain; charset=utf-8")
		if store.deleteCalls != 1 || store.deletedOrg != "alpha" || store.deletedID != 4 {
			t.Fatalf("delete calls=%d values=%q/%d", store.deleteCalls, store.deletedOrg, store.deletedID)
		}
	})
}

func TestRoundEndpointMethodRestrictions(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			handler := NewRoundHandlers(&fakeRoundStore{}, NewResponder(nil))
			assertRoundHTTP(t, handler, method, "/round?org=alpha", http.StatusMethodNotAllowed, `{"Message":"Only POST and DELETE requests are allowed at this route"}`, "")
		})
	}
}

func TestRoundEndpointRepositoryFailures(t *testing.T) {
	t.Run("POST add", func(t *testing.T) {
		handler := NewRoundHandlers(&fakeRoundStore{addErr: errors.New("add failure")}, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=alpha&round=now", http.StatusBadRequest, `{"Message":"add failure"}`, "")
	})
	t.Run("POST reschedule", func(t *testing.T) {
		handler := NewRoundHandlers(&fakeRoundStore{rescheduleErr: errors.New("reschedule failure")}, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=alpha&round=now&roundId=1", http.StatusInternalServerError, `{"Message":"reschedule failure"}`, "")
	})
	t.Run("DELETE", func(t *testing.T) {
		handler := NewRoundHandlers(&fakeRoundStore{deleteErr: errors.New("delete failure")}, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodDelete, "/round?org=alpha&roundId=1", http.StatusInternalServerError, `{"Message":"delete failure"}`, "")
	})
}

func TestRoundHandlersMutationInputFailures(t *testing.T) {
	tests := []struct {
		name   string
		method string
		target string
		store  RoundStore
		status int
		body   string
	}{
		{"add missing org", http.MethodPost, "/round?round=now", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain org"}`},
		{"add duplicate org", http.MethodPost, "/round?org=one&org=two&round=now", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain org"}`},
		{"add missing round", http.MethodPost, "/round?org=alpha", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain round"}`},
		{"add duplicate round", http.MethodPost, "/round?org=alpha&round=one&round=two", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain round"}`},
		{"reschedule empty id", http.MethodPost, "/round?org=alpha&round=now&roundId=", &fakeRoundStore{}, http.StatusInternalServerError, `{"Message":"strconv.Atoi: parsing \"\": invalid syntax"}`},
		{"reschedule invalid id", http.MethodPost, "/round?org=alpha&round=now&roundId=nope", &fakeRoundStore{}, http.StatusInternalServerError, `{"Message":"strconv.Atoi: parsing \"nope\": invalid syntax"}`},
		{"reschedule missing round", http.MethodPost, "/round?org=alpha&roundId=1", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain round"}`},
		{"reschedule duplicate round", http.MethodPost, "/round?org=alpha&round=one&round=two&roundId=1", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain round"}`},
		{"remove missing id", http.MethodDelete, "/round?org=alpha", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain roundId"}`},
		{"remove duplicate org", http.MethodDelete, "/round?org=one&org=two&roundId=1", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain org"}`},
		{"remove duplicate id", http.MethodDelete, "/round?org=alpha&roundId=1&roundId=2", &fakeRoundStore{}, http.StatusBadRequest, `{"Message":"Request query parameters does not contain roundId"}`},
		{"remove empty id", http.MethodDelete, "/round?org=alpha&roundId=", &fakeRoundStore{}, http.StatusInternalServerError, `{"Message":"strconv.Atoi: parsing \"\": invalid syntax"}`},
		{"remove invalid id", http.MethodDelete, "/round?org=alpha&roundId=nope", &fakeRoundStore{}, http.StatusInternalServerError, `{"Message":"strconv.Atoi: parsing \"nope\": invalid syntax"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := NewRoundHandlers(test.store, NewResponder(nil))
			assertRoundHTTP(t, handler, test.method, test.target, test.status, test.body, "")
		})
	}
}

func TestRoundHandlersAcceptEmptyMutationValuesOtherThanIDs(t *testing.T) {
	t.Run("add", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=&round=", http.StatusOK, `{"Message":"Successfully scheduled a new round"}`, "text/plain; charset=utf-8")
		if store.addedOrg != "" || store.addedDate != "" {
			t.Fatalf("add values = %q/%q, want empty", store.addedOrg, store.addedDate)
		}
	})
	t.Run("reschedule", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodPost, "/round?org=&round=&roundId=0", http.StatusOK, `{"Message":"Succesfully changed date of the round"}`, "text/plain; charset=utf-8")
		if store.rescheduledOrg != "" || store.rescheduledDate != "" || store.rescheduledID != 0 {
			t.Fatalf("reschedule values = %q/%q/%d, want empty/empty/0", store.rescheduledOrg, store.rescheduledDate, store.rescheduledID)
		}
	})
	t.Run("remove", func(t *testing.T) {
		store := &fakeRoundStore{}
		handler := NewRoundHandlers(store, NewResponder(nil))
		assertRoundHTTP(t, handler, http.MethodDelete, "/round?org=&roundId=0", http.StatusOK, `{"Message":"Successfully removed round"}`, "text/plain; charset=utf-8")
		if store.deletedOrg != "" || store.deletedID != 0 {
			t.Fatalf("delete values = %q/%d, want empty/0", store.deletedOrg, store.deletedID)
		}
	})
}

func TestRoundHandlersIndividualMethodErrors(t *testing.T) {
	handlers := NewRoundHandlers(&fakeRoundStore{}, NewResponder(nil))
	assertRoundHTTP(t, http.HandlerFunc(handlers.Add), http.MethodGet, "/round", http.StatusMethodNotAllowed, `{"Message":"Only POST requests are allowed at this route"}`, "")
	assertRoundHTTP(t, http.HandlerFunc(handlers.Remove), http.MethodPost, "/round", http.StatusMethodNotAllowed, `{"Message":"Only DELETE requests are allowed at this route"}`, "")
	assertRoundHTTP(t, http.HandlerFunc(handlers.Reschedule), http.MethodGet, "/round", http.StatusMethodNotAllowed, "Only POST requests are allowed at this route", "")
}
