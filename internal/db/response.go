package db

import "encoding/json"

// Message is the JSON body used for simple messages and errors.
type Message struct {
	Message string
}

// StrToBytes encodes message as a JSON Message body.
func StrToBytes(message string) []byte {
	bytes, _ := json.Marshal(Message{message})
	return bytes
}

// ErrToBytes encodes err as a JSON Message body.
func ErrToBytes(err error) []byte {
	bytes, _ := json.Marshal(Message{err.Error()})
	return bytes
}
