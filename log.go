package mealbot

import (
	"log/slog"
	"net/http"

	"github.com/johnamadeo/mealbot/internal/httpapi"
)

// legacyResponder is confined to the compatibility package. New transport
// code receives its logger explicitly and has no package-global logging state.
var legacyResponder = httpapi.NewResponder(slog.Default())

func LogAndWriteErr(w http.ResponseWriter, err error, status int, function string) {
	legacyResponder.WriteError(w, err, status, function)
}

func LogAndWrite(w http.ResponseWriter, bytes []byte, status int, function string) {
	legacyResponder.Write(w, bytes, status, function)
}

func LogAndWriteStatusBadRequest(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusBadRequest, function)
}

func LogAndWriteStatusInternalServerError(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusInternalServerError, function)
}
