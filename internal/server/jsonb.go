package server

import (
	"database/sql/driver"
	"errors"
)

// JSONB is a raw JSON document stored in a Postgres JSONB column.
// Originally copied from https://github.com/jinzhu/gorm/issues/516#issuecomment-109055198
type JSONB []byte

// Value implements driver.Valuer. The document is sent as text so Postgres
// parses it as JSON; empty and "null" documents are stored as SQL NULL.
func (j JSONB) Value() (driver.Value, error) {
	if j.IsNull() {
		return nil, nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner; drivers may hand back either text or bytes.
func (j *JSONB) Scan(value interface{}) error {
	switch v := value.(type) {
	case nil:
		*j = nil
	case []byte:
		// the driver may reuse the slice, so copy it
		*j = append((*j)[0:0], v...)
	case string:
		*j = append((*j)[0:0], v...)
	default:
		return errors.New("JSONB scan source was not string or []byte")
	}
	return nil
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

// IsNull reports whether j is empty or the JSON null literal.
func (j JSONB) IsNull() bool {
	return len(j) == 0 || string(j) == "null"
}
