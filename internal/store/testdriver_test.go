package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
)

type ledgerEntry struct {
	name     string
	checksum string
}

type driverState struct {
	mu           sync.Mutex
	events       []string
	applied      map[int64]ledgerEntry
	queryColumns []string
	queryValues  [][]driver.Value
	queryRowsErr error
	failSQL      string
	beginErr     error
	commitErr    error
	rollbackErr  error
}

func (s *driverState) event(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, value)
}

func (s *driverState) snapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]string, len(s.events))
	copy(result, s.events)
	return result
}

type testDriver struct{ state *driverState }

func (d *testDriver) Open(string) (driver.Conn, error) {
	return &testConn{state: d.state}, nil
}

type testConn struct{ state *driverState }

func (c *testConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by test driver")
}

func (c *testConn) Close() error { return nil }

func (c *testConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}

func (c *testConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	c.state.event("begin")
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	return &testTx{state: c.state}, nil
}

func (c *testConn) Ping(context.Context) error { return nil }

func (c *testConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	compact := strings.Join(strings.Fields(query), " ")
	c.state.event("exec:" + compact)
	if c.state.failSQL != "" && strings.Contains(query, c.state.failSQL) {
		return nil, errors.New("injected execution failure")
	}
	if strings.Contains(query, "INSERT INTO schema_migrations") {
		version := args[0].Value.(int64)
		c.state.mu.Lock()
		if c.state.applied == nil {
			c.state.applied = make(map[int64]ledgerEntry)
		}
		c.state.applied[version] = ledgerEntry{
			name:     args[1].Value.(string),
			checksum: args[2].Value.(string),
		}
		c.state.mu.Unlock()
	}
	return driver.RowsAffected(1), nil
}

func (c *testConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	compact := strings.Join(strings.Fields(query), " ")
	c.state.event("query:" + compact)
	if c.state.failSQL != "" && strings.Contains(query, c.state.failSQL) {
		return nil, errors.New("injected query failure")
	}
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	if strings.Contains(query, "FROM members") && c.state.queryColumns != nil {
		return &testRows{
			columns:     append([]string(nil), c.state.queryColumns...),
			values:      append([][]driver.Value(nil), c.state.queryValues...),
			terminalErr: c.state.queryRowsErr,
		}, nil
	}
	if strings.Contains(query, "WHERE version = $1") {
		version := args[0].Value.(int64)
		entry, ok := c.state.applied[version]
		if !ok {
			return &testRows{columns: []string{"name", "checksum"}}, nil
		}
		return &testRows{
			columns: []string{"name", "checksum"},
			values:  [][]driver.Value{{entry.name, entry.checksum}},
		}, nil
	}
	values := make([][]driver.Value, 0, len(c.state.applied))
	for version, entry := range c.state.applied {
		values = append(values, []driver.Value{version, entry.name, entry.checksum})
	}
	return &testRows{
		columns: []string{"version", "name", "checksum"},
		values:  values,
	}, nil
}

type testTx struct{ state *driverState }

func (tx *testTx) Commit() error {
	tx.state.event("commit")
	return tx.state.commitErr
}

func (tx *testTx) Rollback() error {
	tx.state.event("rollback")
	return tx.state.rollbackErr
}

type testRows struct {
	columns     []string
	values      [][]driver.Value
	index       int
	terminalErr error
	errReturned bool
}

func (r *testRows) Columns() []string { return r.columns }
func (r *testRows) Close() error      { return nil }

func (r *testRows) Next(destination []driver.Value) error {
	if r.index >= len(r.values) {
		if r.terminalErr != nil && !r.errReturned {
			r.errReturned = true
			return r.terminalErr
		}
		return io.EOF
	}
	copy(destination, r.values[r.index])
	r.index++
	return nil
}

var testDriverSequence atomic.Uint64

func openDriverDB(state *driverState) *sql.DB {
	if state.applied == nil {
		state.applied = make(map[int64]ledgerEntry)
	}
	name := fmt.Sprintf("mealbot-store-test-%d", testDriverSequence.Add(1))
	sql.Register(name, &testDriver{state: state})
	db, err := sql.Open(name, "")
	if err != nil {
		panic(err)
	}
	return db
}
