package db

import (
	"database/sql/driver"
	"errors"
)

// JSONB is used for inserting/retrieving JSONB columns from a Postgres database.
// Copied from https://github.com/jinzhu/gorm/issues/516#issuecomment-109055198
type JSONB []byte

// Value implements driver.Valuer.
func (j JSONB) Value() (driver.Value, error) {
	if j.IsNull() {
		return nil, nil
	}
	return string(j), nil
}

// Scan implements sql.Scanner. Both []byte and string sources are accepted
// since drivers differ in how they surface JSONB values.
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
		return errors.New("Scan source was not string")
	}

	// The byte slice passed in may be re-used by the driver, so copy it.
	*j = append((*j)[0:0], s...)
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
		return errors.New("json.RawMessage: UnmarshalJSON on nil pointer")
	}
	*j = append((*j)[0:0], data...)
	return nil
}

// IsNull reports whether the value is empty or the JSON literal null.
func (j JSONB) IsNull() bool {
	return len(j) == 0 || string(j) == "null"
}
