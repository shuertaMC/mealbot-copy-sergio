package mealbot

import (
	"database/sql"

	internalstore "github.com/johnamadeo/mealbot/internal/store"
	"github.com/johnamadeo/server"
)

// openStoreAdapter is the explicit migration seam for legacy root handlers.
// New composition code injects Store directly instead of opening per request.
func openStoreAdapter() (*internalstore.Store, *sql.DB, error) {
	db, err := server.CreateDBConnection(LocalDBConnection)
	if err != nil {
		return nil, nil, err
	}
	repository, err := internalstore.New(db)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return repository, db, nil
}
