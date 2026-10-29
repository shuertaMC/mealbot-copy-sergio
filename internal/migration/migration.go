// Package migration holds one-off data migrations run through `mealbot migrate`.
package migration

import (
	"context"
	"log/slog"

	"github.com/johnamadeo/mealbot/internal/store"
)

// MigrateToLastRoundWith rebuilds every active member's 'last round with'
// history from the pairs stored for their organization.
func MigrateToLastRoundWith(ctx context.Context, st *store.Store, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}

	organizations, err := st.ListOrganizations(ctx)
	if err != nil {
		return err
	}

	for _, org := range organizations {
		members, err := st.GetMembers(ctx, org, true)
		if err != nil {
			return err
		}

		history := map[string]map[string]int{}
		for _, member := range members {
			history[member.Email] = map[string]int{}
			// -1 means "never paired", which keeps them eligible as partners
			for _, other := range members {
				if other.Email != member.Email {
					history[member.Email][other.Email] = -1
				}
			}
		}

		pairs, err := st.GetPairs(ctx, org)
		if err != nil {
			return err
		}

		for round, roundPairs := range pairs {
			for _, pair := range roundPairs {
				RecordPair(history, pair, round)
			}
		}

		if err := st.UpdateLastRoundWith(ctx, org, history); err != nil {
			return err
		}
		logger.Info("migrated organization", "organization", org, "members", len(members))
	}

	logger.Info("Done migrating to 'last round with' data structure in pairing algorithm!")
	return nil
}

// RecordPair notes in history that the members of pair were grouped in round.
// Members that are not in history (i.e. no longer active) are ignored.
func RecordPair(history map[string]map[string]int, pair store.PairRecord, round int) {
	link := func(a, b string) {
		if _, ok := history[a]; !ok {
			return
		}
		if _, ok := history[b]; !ok {
			return
		}
		history[a][b] = round
	}

	link(pair.ID1, pair.ID2)
	link(pair.ID2, pair.ID1)
	if pair.ExtraID != "" {
		link(pair.ID1, pair.ExtraID)
		link(pair.ID2, pair.ExtraID)
		link(pair.ExtraID, pair.ID1)
		link(pair.ExtraID, pair.ID2)
	}
}
