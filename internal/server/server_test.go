package server

import (
	"errors"
	"testing"
)

func TestStrAndErrToBytes(t *testing.T) {
	if got := string(StrToBytes("hello")); got != `{"Message":"hello"}` {
		t.Errorf("StrToBytes = %s", got)
	}
	if got := string(ErrToBytes(errors.New("boom"))); got != `{"Message":"boom"}` {
		t.Errorf("ErrToBytes = %s", got)
	}
}

func TestJSONBScanAndValue(t *testing.T) {
	var j JSONB
	if err := j.Scan([]byte(`{"a":1}`)); err != nil || string(j) != `{"a":1}` {
		t.Fatalf("scan bytes: %v %s", err, j)
	}
	if err := j.Scan(`{"b":2}`); err != nil || string(j) != `{"b":2}` {
		t.Fatalf("scan string: %v %s", err, j)
	}
	if err := j.Scan(42); err == nil {
		t.Fatal("expected error scanning an int")
	}
	if err := j.Scan(nil); err != nil || !j.IsNull() {
		t.Fatalf("scan nil: %v", err)
	}

	v, err := JSONB(`{"a":1}`).Value()
	if err != nil || v != `{"a":1}` {
		t.Errorf("value = %v, %v", v, err)
	}
	if v, _ := JSONB(nil).Value(); v != nil {
		t.Errorf("nil JSONB should be SQL NULL, got %v", v)
	}
}

func TestCreateDBConnection(t *testing.T) {
	db, err := CreateDBConnection("")
	if !errors.Is(err, ErrNoDatabaseURL) || db != nil {
		t.Fatalf("expected ErrNoDatabaseURL and nil db, got %v, %v", db, err)
	}

	// sql.Open is lazy, so this does not need a running database
	db, err = CreateDBConnection("postgres://user:pass@localhost:5432/mealbot?sslmode=disable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}
