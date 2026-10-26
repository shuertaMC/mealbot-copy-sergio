package main

import (
	"net/http"

	"github.com/johnamadeo/mealbot/internal/server"
	log "github.com/sirupsen/logrus"
)

func LogAndWriteErr(w http.ResponseWriter, err error, status int, function string) {
	log.WithFields(log.Fields{
		"logger":   "logrus",
		"status":   status,
		"function": function,
	}).Error(err)
	w.WriteHeader(status)
	w.Write(server.ErrToBytes(err))
}

func LogAndWrite(w http.ResponseWriter, bytes []byte, status int, function string) {
	log.WithFields(log.Fields{
		"logger":   "logrus",
		"function": function,
	}).Debug(status)
	w.Write(bytes)
}

func LogAndWriteStatusBadRequest(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusBadRequest, function)
}

func LogAndWriteStatusInternalServerError(w http.ResponseWriter, err error, function string) {
	LogAndWriteErr(w, err, http.StatusInternalServerError, function)
}
