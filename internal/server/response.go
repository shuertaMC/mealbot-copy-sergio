package server

import "encoding/json"

func StrToBytes(message string) []byte {
	bytes, _ := json.Marshal(Message{message})
	return bytes
}

func ErrToBytes(err error) []byte {
	bytes, _ := json.Marshal(Message{err.Error()})
	return bytes
}
