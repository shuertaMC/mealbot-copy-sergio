package store

import (
	"context"
	"database/sql"
)

// PairRecord is one stored pairing. ExtraID is empty when there is no third member.
type PairRecord struct {
	ID1     string
	ID2     string
	ExtraID string
}

// GetPairs returns the stored pairings of an organization grouped by round,
// starting at round 0 and stopping at the first round without pairings.
func (s *Store) GetPairs(ctx context.Context, orgname string) ([][]PairRecord, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT round, id1, id2, extraId FROM pairs WHERE organization = $1 ORDER BY round, id1, id2, extraId",
		orgname,
	)
	if err != nil {
		return [][]PairRecord{}, err
	}
	defer rows.Close()

	byRound := map[int][]PairRecord{}
	for rows.Next() {
		var round int
		var id1, id2 string
		var extraID sql.NullString
		if err := rows.Scan(&round, &id1, &id2, &extraID); err != nil {
			return [][]PairRecord{}, err
		}

		byRound[round] = append(byRound[round], PairRecord{
			ID1:     id1,
			ID2:     id2,
			ExtraID: extraID.String, // "" when NULL
		})
	}
	if err := rows.Err(); err != nil {
		return [][]PairRecord{}, err
	}

	roundPairs := [][]PairRecord{}
	for round := 0; len(byRound[round]) > 0; round++ {
		roundPairs = append(roundPairs, byRound[round])
	}
	return roundPairs, nil
}

// SaveRound records the pairs of a round, stores the updated pairing history of
// the members and marks the round as done, all in one transaction.
func (s *Store) SaveRound(
	ctx context.Context,
	orgname string,
	roundNum int,
	pairs []PairRecord,
	lastRoundWith map[string]map[string]int,
) error {
	return s.inTx(ctx, func(q DBTX) error {
		for _, pair := range pairs {
			_, err := q.ExecContext(
				ctx,
				"INSERT INTO pairs (organization, id1, id2, extraId, round) VALUES ($1, $2, $3, $4, $5)",
				orgname,
				pair.ID1,
				pair.ID2,
				pair.ExtraID,
				roundNum,
			)
			if err != nil {
				return err
			}
		}

		if err := updateLastRoundWith(ctx, q, orgname, lastRoundWith); err != nil {
			return err
		}

		_, err := q.ExecContext(
			ctx,
			"UPDATE rounds SET done = $1 WHERE organization = $2 AND id = $3",
			true,
			orgname,
			roundNum,
		)
		return err
	})
}
