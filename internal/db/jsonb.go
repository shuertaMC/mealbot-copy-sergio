package db

import (
	"bytes"
	"database/sql/driver"
	"errors"
)

// JSONB is a helper type for inserting/retrieving JSONB values.
// Originally copied from https://github.com/jinzhu/gorm/issues/516#issuecomment-109055198
//
// With pgx v5, pass JSONB values as query arguments via String() (or a plain
// string/[]byte); pgx sends strings in text format which Postgres parses as
// jsonb. When scanning, scan into a []byte and convert with JSONB(raw).
type JSONB []byte

// Value implements driver.Valuer.
func (j JSONB) Value() (driver.Value, error) {
	if j.IsNull() {
		return nil, nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner. pgx may hand over a string or a []byte.
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = nil
		return nil
	}
	var s []byte
	switch v := value.(type) {
	case []byte:
		s = v
	case string:
		s = []byte(v)
	default:
		return errors.New("Scan source was not string or []byte")
	}
	// Make a copy of the bytes since the passed in slice may be re-used
	*j = append((*j)[0:0], s...)
	return nil
}

// String returns the JSON text, suitable as a query argument for a jsonb
// column. A null/empty value yields "null".
func (j JSONB) String() string {
	if len(j) == 0 {
		return "null"
	}
	return string(j)
}

// MarshalJSON returns j as the JSON encoding of itself.
func (j JSONB) MarshalJSON() ([]byte, error) {
	if j == nil {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON sets *j to a copy of data.
func (j *JSONB) UnmarshalJSON(data []byte) error {
	if j == nil {
		return errors.New("JSONB: UnmarshalJSON on nil pointer")
	}
	*j = append((*j)[0:0], data...)
	return nil
}

// IsNull reports whether the value is empty or the JSON literal null.
func (j JSONB) IsNull() bool {
	return len(j) == 0 || string(j) == "null"
}

// Equals reports whether two JSONB values hold identical bytes.
func (j JSONB) Equals(j1 JSONB) bool {
	return bytes.Equal([]byte(j), []byte(j1))
}
