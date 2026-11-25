package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewRequiresInjectedDatabase(t *testing.T) {
	t.Parallel()
	if _, err := New(nil); !errors.Is(err, ErrNilDatabase) {
		t.Fatalf("New(nil) error = %v, want ErrNilDatabase", err)
	}
	if _, err := Open(context.Background(), ""); !errors.Is(err, ErrDatabaseURLRequired) {
		t.Fatalf("Open with empty URL error = %v, want ErrDatabaseURLRequired", err)
	}
}

func TestNormalizeConnectionOptionsAppliesSanePoolDefaults(t *testing.T) {
	t.Parallel()
	options := normalizeConnectionOptions(ConnectionOptions{})
	if options.MaxOpenConnections <= 0 || options.MaxIdleConnections <= 0 {
		t.Fatalf("pool sizes are not positive: %+v", options)
	}
	if options.MaxIdleConnections > options.MaxOpenConnections {
		t.Fatalf("idle connections exceed open connections: %+v", options)
	}
	if options.ConnectionLifetime <= 0 || options.ConnectionIdleTime <= 0 || options.PingTimeout <= 0 {
		t.Fatalf("timeouts are not finite: %+v", options)
	}
}

func TestWithinTransactionCommits(t *testing.T) {
	state := &driverState{}
	db := openDriverDB(state)
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithinTransaction(context.Background(), func(DBTX) error { return nil }); err != nil {
		t.Fatalf("WithinTransaction() error = %v", err)
	}
	if got := strings.Join(state.snapshot(), ","); got != "begin,commit" {
		t.Fatalf("events = %q, want begin,commit", got)
	}
}

func TestWithinTransactionRollsBackAndPropagatesBothErrors(t *testing.T) {
	callbackErr := errors.New("callback failed")
	rollbackErr := errors.New("rollback failed")
	state := &driverState{rollbackErr: rollbackErr}
	db := openDriverDB(state)
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithinTransaction(context.Background(), func(DBTX) error { return callbackErr })
	if !errors.Is(err, callbackErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("error = %v, want callback and rollback errors", err)
	}
	if got := strings.Join(state.snapshot(), ","); got != "begin,rollback" {
		t.Fatalf("events = %q, want begin,rollback", got)
	}
}

func TestWithinTransactionPropagatesCommitError(t *testing.T) {
	commitErr := errors.New("commit failed")
	state := &driverState{commitErr: commitErr}
	db := openDriverDB(state)
	defer db.Close()
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithinTransaction(context.Background(), func(DBTX) error { return nil })
	if !errors.Is(err, commitErr) {
		t.Fatalf("error = %v, want commit error", err)
	}
}
