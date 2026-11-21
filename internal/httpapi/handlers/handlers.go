// Package handlers contains the HTTP handlers of the Mealbot API.
package handlers

import (
	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/db"
	"github.com/johnamadeo/mealbot/internal/mail"
	"github.com/johnamadeo/mealbot/internal/store"
)

// Handler holds the dependencies shared by all handlers.
type Handler struct {
	DB     db.Querier
	Store  *store.Store
	Mailer mail.Mailer
	Config config.Config
}

// New creates a Handler. q is typically a *pgxpool.Pool.
func New(q db.Querier, mailer mail.Mailer, cfg config.Config) *Handler {
	return &Handler{
		DB:     q,
		Store:  store.New(q),
		Mailer: mailer,
		Config: cfg,
	}
}
