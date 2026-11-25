package mealbot

import (
	"context"
	"fmt"
)

func migrateToLastRoundWithForPairing() error {
	repository, db, err := openStoreAdapter()
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := repository.ReconstructPairHistory(context.Background())
	if err != nil {
		return err
	}
	if !result.Completed {
		return fmt.Errorf("pair-history reconstruction did not complete")
	}

	fmt.Println("Done migrating to 'last round with' data structure in pairing algorithm!")
	return nil
}
