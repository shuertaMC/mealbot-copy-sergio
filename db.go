package mealbot

import (
	"context"
	"database/sql"
	"os"

	internalstore "github.com/johnamadeo/mealbot/internal/store"
)

// DuplicateKeyErr : Error message when user tries to insert row with duplicate key
const DuplicateKeyErr = "duplicate key value violates unique constraint"

// openLegacyDatabase keeps the characterization-only root adapters on the
// same pgx/v5 connection path as the production composition root. Production
// startup is owned exclusively by cmd/mealbot.
func openLegacyDatabase() (*sql.DB, error) {
	return internalstore.Open(context.Background(), os.Getenv("DATABASE_URL"))
}
