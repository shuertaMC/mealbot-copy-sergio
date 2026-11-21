package db

import (
	"encoding/json"
	"testing"
)

func TestLocalDSN(t *testing.T) {
	got := LocalDBConnection{User: "u", DBName: "d"}.DSN()
	if got != "user=u dbname=d sslmode=disable" {
		t.Fatalf("dsn = %q", got)
	}
}

func TestJSONBArgAndValue(t *testing.T) {
	if JSONB(nil).Arg() != nil || JSONB("null").Arg() != nil {
		t.Fatal("null JSONB must map to SQL NULL")
	}
	if got := JSONB(`{"a":1}`).Arg(); got != `{"a":1}` {
		t.Fatalf("arg = %v", got)
	}
	v, err := JSONB(`{"a":1}`).Value()
	if err != nil || v != `{"a":1}` {
		t.Fatalf("value = %v, %v", v, err)
	}
}

func TestJSONBScan(t *testing.T) {
	var j JSONB
	if err := j.Scan([]byte(`{"a":1}`)); err != nil || string(j) != `{"a":1}` {
		t.Fatalf("scan bytes: %v %q", err, j)
	}
	if err := j.Scan(`{"b":2}`); err != nil || string(j) != `{"b":2}` {
		t.Fatalf("scan string: %v %q", err, j)
	}
	if err := j.Scan(nil); err != nil || j != nil {
		t.Fatalf("scan nil: %v %v", err, j)
	}
	if err := j.Scan(42); err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestJSONBMarshal(t *testing.T) {
	b, err := JSONB(nil).MarshalJSON()
	if err != nil || string(b) != "null" {
		t.Fatalf("%v %q", err, b)
	}
	if !JSONB(`{"a":1}`).Equals(JSONB(`{"a":1}`)) {
		t.Fatal("Equals")
	}
}

func TestMessageBodies(t *testing.T) {
	if string(StrToBytes("hi")) != `{"Message":"hi"}` {
		t.Fatalf("%s", StrToBytes("hi"))
	}
	var m Message
	if err := json.Unmarshal(ErrToBytes(errString("boom")), &m); err != nil || m.Message != "boom" {
		t.Fatalf("%v %+v", err, m)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
