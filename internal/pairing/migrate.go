package pairing

import (
	"context"
	"log/slog"

	"github.com/johnamadeo/mealbot/internal/store"
)

// MigrateToLastRoundWith rebuilds the 'last round with' data structure used by
// the pairing algorithm from the stored pairs of every organization.
func MigrateToLastRoundWith(ctx context.Context, st *store.Store) error {
	organizations, err := st.GetAllOrganizations(ctx)
	if err != nil {
		return err
	}

	for _, org := range organizations {
		membersMap := map[string]store.Member{}
		members, err := st.GetMembers(ctx, org, true)
		if err != nil {
			return err
		}

		for _, member := range members {
			member.LastRoundWith = map[string]int{}
			membersMap[member.Email] = member
		}

		pairs, err := st.GetPairs(ctx, org)
		if err != nil {
			return err
		}

		for round, roundPairs := range pairs {
			for _, pairing := range roundPairs {
				member1Email := pairing.Member1.Email
				member2Email := pairing.Member2.Email
				extraMemberEmail := pairing.ExtraMember.Email

				// Need to check if member in the pairing is still active
				if _, ok := membersMap[member1Email]; ok {
					membersMap[member1Email].LastRoundWith[member2Email] = round
				}
				if _, ok := membersMap[member2Email]; ok {
					membersMap[member2Email].LastRoundWith[member1Email] = round
				}
				if _, ok := membersMap[extraMemberEmail]; ok {
					membersMap[member1Email].LastRoundWith[extraMemberEmail] = round
					membersMap[member2Email].LastRoundWith[extraMemberEmail] = round
					membersMap[extraMemberEmail].LastRoundWith[member1Email] = round
					membersMap[extraMemberEmail].LastRoundWith[member2Email] = round
				}
			}
		}

		for _, member := range membersMap {
			if err := st.UpdateLastRoundWith(ctx, org, member.Email, member.LastRoundWith); err != nil {
				return err
			}
		}
	}

	slog.Info("Done migrating to 'last round with' data structure in pairing algorithm!")
	return nil
}
