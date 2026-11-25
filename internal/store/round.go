package store

import (
	"context"
	"fmt"
	"time"

	"github.com/johnamadeo/mealbot/internal/pairing"
)

const legacyRoundTimestampFormat = `YYYY-MM-DD HH24:MI:ss"Z"`

// ListRounds returns scheduled timestamps in the legacy UTC display format and
// per-organization round order.
func (s *Store) ListRounds(ctx context.Context, organization string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(opCtx, `
SELECT to_char(scheduled_date, $1)
FROM rounds
WHERE organization = $2
ORDER BY id
`, legacyRoundTimestampFormat, organization)
	if err != nil {
		return nil, fmt.Errorf("list rounds: %w", err)
	}
	defer rows.Close()

	rounds := make([]string, 0)
	for rows.Next() {
		var scheduled string
		if err := rows.Scan(&scheduled); err != nil {
			return nil, fmt.Errorf("scan round: %w", err)
		}
		rounds = append(rounds, scheduled)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rounds: %w", err)
	}
	return rounds, nil
}

// AddRound atomically allocates the next non-negative ID for an organization.
// The schedule is accepted as text so PostgreSQL retains the legacy timestamp
// parsing contract used by the HTTP endpoint.
func (s *Store) AddRound(ctx context.Context, organization, scheduled string) (int, error) {
	if s == nil || s.db == nil {
		return 0, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	var roundID int
	err := s.WithinTransaction(opCtx, func(tx DBTX) error {
		if err := lockOrganization(opCtx, tx, organization); err != nil {
			return err
		}
		if err := tx.QueryRowContext(opCtx, `
SELECT COALESCE(MAX(id), -1) + 1
FROM rounds
WHERE organization = $1
`, organization).Scan(&roundID); err != nil {
			return fmt.Errorf("allocate round ID: %w", err)
		}
		if roundID < 0 {
			return fmt.Errorf("allocate round ID: got negative ID %d", roundID)
		}
		if _, err := tx.ExecContext(opCtx, `
INSERT INTO rounds (organization, id, scheduled_date, done)
VALUES ($1, $2, $3, FALSE)
`, organization, roundID, scheduled); err != nil {
			return fmt.Errorf("insert round: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return roundID, nil
}

// RescheduleRound atomically changes one scheduled timestamp.
func (s *Store) RescheduleRound(ctx context.Context, organization, scheduled string, roundID int) error {
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	return s.WithinTransaction(opCtx, func(tx DBTX) error {
		if err := lockOrganization(opCtx, tx, organization); err != nil {
			return err
		}
		if _, err := tx.ExecContext(opCtx, `
UPDATE rounds
SET scheduled_date = $1
WHERE organization = $2 AND id = $3
`, scheduled, organization, roundID); err != nil {
			return fmt.Errorf("reschedule round: %w", err)
		}
		return nil
	})
}

type persistedRound struct {
	id        int
	scheduled time.Time
	done      bool
}

// DeleteRound removes a round and shifts every later round down by one. Pair
// rows move with their rounds, avoiding reliance on cascading foreign keys.
func (s *Store) DeleteRound(ctx context.Context, organization string, roundID int) error {
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	return s.WithinTransaction(opCtx, func(tx DBTX) error {
		if err := lockOrganization(opCtx, tx, organization); err != nil {
			return err
		}
		rows, err := tx.QueryContext(opCtx, `
SELECT id, scheduled_date, done
FROM rounds
WHERE organization = $1 AND id > $2
ORDER BY id
FOR UPDATE
`, organization, roundID)
		if err != nil {
			return fmt.Errorf("load later rounds: %w", err)
		}
		later := make([]persistedRound, 0)
		for rows.Next() {
			var round persistedRound
			if err := rows.Scan(&round.id, &round.scheduled, &round.done); err != nil {
				rows.Close()
				return fmt.Errorf("scan later round: %w", err)
			}
			later = append(later, round)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate later rounds: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close later rounds: %w", err)
		}

		if _, err := tx.ExecContext(opCtx,
			`DELETE FROM pairs WHERE organization = $1 AND round = $2`,
			organization, roundID,
		); err != nil {
			return fmt.Errorf("delete round pairs: %w", err)
		}
		if _, err := tx.ExecContext(opCtx,
			`DELETE FROM rounds WHERE organization = $1 AND id = $2`,
			organization, roundID,
		); err != nil {
			return fmt.Errorf("delete round: %w", err)
		}

		for _, round := range later {
			newID := round.id - 1
			if _, err := tx.ExecContext(opCtx, `
INSERT INTO rounds (organization, id, scheduled_date, done)
VALUES ($1, $2, $3, $4)
`, organization, newID, round.scheduled, round.done); err != nil {
				return fmt.Errorf("insert renumbered round %d: %w", newID, err)
			}
			if _, err := tx.ExecContext(opCtx, `
UPDATE pairs
SET round = $1
WHERE organization = $2 AND round = $3
`, newID, organization, round.id); err != nil {
				return fmt.Errorf("renumber pairs for round %d: %w", round.id, err)
			}
			if _, err := tx.ExecContext(opCtx,
				`DELETE FROM rounds WHERE organization = $1 AND id = $2`,
				organization, round.id,
			); err != nil {
				return fmt.Errorf("remove old round %d: %w", round.id, err)
			}
		}
		return nil
	})
}

func lockOrganization(ctx context.Context, tx DBTX, organization string) error {
	var locked string
	if err := tx.QueryRowContext(ctx,
		`SELECT name FROM organizations WHERE name = $1 FOR UPDATE`,
		organization,
	).Scan(&locked); err != nil {
		return fmt.Errorf("lock organization: %w", err)
	}
	return nil
}

// DueRounds implements pairing.SchedulerStore. A round is due only when its
// stored UTC schedule is strictly before the supplied boundary.
func (s *Store) DueRounds(ctx context.Context, before time.Time) ([]pairing.ScheduledRound, error) {
	if s == nil || s.db == nil {
		return nil, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(opCtx, `
SELECT organization, id
FROM rounds
WHERE scheduled_date < ($1::timestamptz AT TIME ZONE 'UTC') AND done = FALSE
ORDER BY scheduled_date, organization, id
`, before.UTC())
	if err != nil {
		return nil, fmt.Errorf("list due rounds: %w", err)
	}
	defer rows.Close()
	rounds := make([]pairing.ScheduledRound, 0)
	for rows.Next() {
		var round pairing.ScheduledRound
		if err := rows.Scan(&round.Organization, &round.Number); err != nil {
			return nil, fmt.Errorf("scan due round: %w", err)
		}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due rounds: %w", err)
	}
	return rounds, nil
}
