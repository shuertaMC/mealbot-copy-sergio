package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/domain"
)

type errorResponse struct {
	Error string `json:"error"`
}

type responder struct {
	logger *slog.Logger
}

func newResponder(logger *slog.Logger) responder {
	if logger == nil {
		logger = slog.Default()
	}
	return responder{logger: logger}
}

func (r responder) json(w http.ResponseWriter, status int, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		r.internalEncodingFailure(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(encoded, '\n'))
}

func (r responder) text(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(value))
}

func (r responder) methodNotAllowed(w http.ResponseWriter, allowed ...string) {
	for index, method := range allowed {
		if index == 0 {
			w.Header().Set("Allow", method)
		} else {
			w.Header().Add("Allow", method)
		}
	}
	r.json(w, http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
}

func (r responder) failure(ctx context.Context, w http.ResponseWriter, operation string, err error) {
	status, message := publicError(err)
	r.logger.ErrorContext(ctx, "HTTP request failed",
		"operation", operation,
		"status", status,
		"error", err,
	)
	r.json(w, status, errorResponse{Error: message})
}

func (r responder) internalEncodingFailure(w http.ResponseWriter, err error) {
	r.logger.Error("HTTP response encoding failed", "error", err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("{\"error\":\"internal server error\"}\n"))
}

func publicError(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest, "invalid request"
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict, "conflict"
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, "not found"
	default:
		return http.StatusInternalServerError, "internal server error"
	}
}
