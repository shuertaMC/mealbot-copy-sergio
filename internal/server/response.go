package server

import "encoding/json"

// Message is the JSON body used for plain text responses and errors.
type Message struct {
	Message string
}

// StrToBytes encodes message as a {"Message": "..."} JSON body.
func StrToBytes(message string) []byte {
	bytes, _ := json.Marshal(Message{message})
	return bytes
}

// ErrToBytes encodes err as a {"Message": "..."} JSON body.
func ErrToBytes(err error) []byte {
	return StrToBytes(err.Error())
}
