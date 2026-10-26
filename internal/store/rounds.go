package store

import (
	"context"
)

// TimestampFormat : Postgres timestamp string template patterns can be found in https://www.postgresql.org/docs/current/functions-formatting.html
const TimestampFormat = "YYYY-MM-DD HH24:MI:ssZ"

// RoundRef identifies a round of an organization.
type RoundRef struct {
	Organization string
	ID           int
}

// GetRounds returns the formatted dates of all rounds of an organization, ordered by round id.
func (s *Store) GetRounds(ctx context.Context, orgname string) ([]string, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT to_char(scheduled_date, $1) FROM rounds WHERE organization = $2 ORDER BY id ASC",
		TimestampFormat,
		orgname,
	)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	rounds := []string{}
	for rows.Next() {
		var roundDate string
		if err := rows.Scan(&roundDate); err != nil {
			return []string{}, err
		}
		rounds = append(rounds, roundDate)
	}
	if err := rows.Err(); err != nil {
		return []string{}, err
	}

	return rounds, nil
}

// AddRound schedules a new round, numbered after the organization's last round.
func (s *Store) AddRound(ctx context.Context, orgname string, roundDate string) error {
	return s.inTx(ctx, func(q DBTX) error {
		var maxRoundID int
		err := q.QueryRowContext(
			ctx,
			"SELECT COALESCE(MAX(id), -1) FROM rounds WHERE organization = $1",
			orgname,
		).Scan(&maxRoundID)
		if err != nil {
			return err
		}

		_, err = q.ExecContext(
			ctx,
			"INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ($1, $2, $3, $4)",
			orgname,
			maxRoundID+1,
			roundDate,
			false,
		)
		return err
	})
}

// RemoveRound deletes a round and shifts the ids of all later rounds down by one.
func (s *Store) RemoveRound(ctx context.Context, orgname string, roundID int) error {
	return s.inTx(ctx, func(q DBTX) error {
		_, err := q.ExecContext(
			ctx,
			"DELETE FROM rounds WHERE organization = $1 AND id = $2",
			orgname,
			roundID,
		)
		if err != nil {
			return err
		}

		for {
			result, err := q.ExecContext(
				ctx,
				"UPDATE rounds SET id = $1 WHERE organization = $2 AND id = $3",
				roundID,
				orgname,
				roundID+1,
			)
			if err != nil {
				return err
			}
			if numRows, _ := result.RowsAffected(); numRows == 0 {
				return nil
			}

			roundID++
		}
	})
}

// RescheduleRound changes the date of an existing round.
func (s *Store) RescheduleRound(ctx context.Context, orgname string, roundDate string, roundID int) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE rounds SET scheduled_date = $1 WHERE organization = $2 AND id = $3",
		roundDate,
		orgname,
		roundID,
	)
	return err
}

// DueRounds returns the rounds scheduled in the past that have not been run yet.
func (s *Store) DueRounds(ctx context.Context) ([]RoundRef, error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT organization, id FROM rounds WHERE scheduled_date < now() AT TIME ZONE 'utc' AND done = false ORDER BY organization, id",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var due []RoundRef
	for rows.Next() {
		var ref RoundRef
		if err := rows.Scan(&ref.Organization, &ref.ID); err != nil {
			return nil, err
		}
		due = append(due, ref)
	}
	return due, rows.Err()
}
