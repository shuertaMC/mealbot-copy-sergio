package server

import (
	"context"
	"errors"
	"testing"
	"time"
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

func TestPing(t *testing.T) {
	// sql.Open is lazy, so this does not need a running database
	db, err := CreateDBConnection("postgres://user:pass@localhost:5432/mealbot?sslmode=disable")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// an already-cancelled parent context must make Ping fail without dialing
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Ping(ctx, db, time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: expected context.Canceled, got %v", err)
	}

	// a closed pool must report an error rather than hang or panic
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := Ping(context.Background(), db, time.Second); err == nil {
		t.Error("expected an error pinging a closed db")
	}
}
