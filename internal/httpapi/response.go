package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Message is the legacy response envelope. The field deliberately has no JSON
// tag: encoding/json must retain the historical capital M in "Message".
type Message struct {
	Message string
}

// MessageBytes encodes a plain message using the legacy response envelope.
func MessageBytes(message string) []byte {
	encoded, _ := json.Marshal(Message{Message: message})
	return encoded
}

// ErrorBytes encodes err using the legacy response envelope.
func ErrorBytes(err error) []byte {
	if err == nil {
		return MessageBytes("")
	}
	return MessageBytes(err.Error())
}

// Responder centralizes compatibility response writing and safe structured
// logging. A nil logger disables logging rather than falling back to process
// global state.
type Responder struct {
	logger *slog.Logger
}

// NewResponder constructs a response writer with an explicitly injected
// logger. The responder never logs response bodies or error text.
func NewResponder(logger *slog.Logger) Responder {
	return Responder{logger: logger}
}

// Cleanup invokes cleanup and reports failures without exposing the underlying
// error or changing an HTTP response. Resource must be a fixed, non-sensitive
// label supplied by the caller rather than a filename or path.
func (r Responder) Cleanup(function, resource string, cleanup func() error) {
	if cleanup == nil {
		return
	}
	if err := cleanup(); err != nil && r.logger != nil {
		r.logger.Error("resource cleanup failed", "function", function, "resource", resource)
	}
}

// Write reproduces the legacy success behavior exactly. status is retained as
// a logging field but is intentionally not passed to WriteHeader: successful
// legacy handlers therefore keep their implicit 200 status and net/http's
// compatible content sniffing behavior.
func (r Responder) Write(w http.ResponseWriter, body []byte, status int, function string) {
	if r.logger != nil {
		r.logger.Debug("http response", "function", function, "status", status)
	}
	_, _ = w.Write(body)
}

// WriteError writes the requested status before the message envelope. Doing so
// deliberately leaves Content-Type unset, matching the characterized service.
// Error details are returned to clients for compatibility but are never logged.
func (r Responder) WriteError(w http.ResponseWriter, err error, status int, function string) {
	if r.logger != nil {
		r.logger.Error("http request failed", "function", function, "status", status)
	}
	w.WriteHeader(status)
	_, _ = w.Write(ErrorBytes(err))
}

// WriteBadRequest is the compatibility shortcut for a 400 response.
func (r Responder) WriteBadRequest(w http.ResponseWriter, err error, function string) {
	r.WriteError(w, err, http.StatusBadRequest, function)
}

// WriteInternalServerError is the compatibility shortcut for a 500 response.
func (r Responder) WriteInternalServerError(w http.ResponseWriter, err error, function string) {
	r.WriteError(w, err, http.StatusInternalServerError, function)
}
