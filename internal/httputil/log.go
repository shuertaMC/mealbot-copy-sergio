package httputil

import (
	"encoding/json"
	"net/http"

	log "github.com/sirupsen/logrus"
)

// Message is the JSON body used for simple textual responses.
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

// LogAndWriteErr logs err and writes it with the given status.
func LogAndWriteErr(w http.ResponseWriter, err error, status int, function string) {
	log.WithFields(log.Fields{
		"logger":   "logrus",
		"status":   status,
		"function": function,
	}).Error(err)
	w.WriteHeader(status)
	w.Write(ErrToBytes(err))
}

// LogAndWrite logs the status and writes bytes as the response body.
func LogAndWrite(w http.ResponseWriter, bytes []byte, status int, function string) {
	log.WithFields(log.Fields{
		"logger":   "logrus",
		"function": function,
	}).Debug(status)
	w.Write(bytes)
}

// LogAndWriteStatusBadRequest logs err and responds 400.
func LogAndWriteStatusBadRequest(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusBadRequest, function)
}

// LogAndWriteStatusInternalServerError logs err and responds 500.
func LogAndWriteStatusInternalServerError(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusInternalServerError, function)
}
