package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/johnamadeo/mealbot/internal/domain"
)

const defaultClaimLease = 15 * time.Minute

// ListRounds returns an organization's rounds ordered by ID.
func (r *Repository) ListRounds(ctx context.Context, organization string) ([]domain.Round, error) {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return nil, domain.NewValidationError("round.organization", "must not be empty")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT organization, id, scheduled_date, done
		FROM rounds WHERE organization = $1 ORDER BY id`, organization)
	if err != nil {
		return nil, mapDatabaseError("list rounds", err)
	}
	defer rows.Close()
	return scanRounds(rows, "list rounds")
}

// AddRound atomically allocates the next organization-local ID using a
// nullable aggregate, so the first round is ID zero.
func (r *Repository) AddRound(ctx context.Context, organization string, scheduledAt time.Time) (domain.Round, error) {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.Round{}, domain.NewValidationError("round.organization", "must not be empty")
	}
	if scheduledAt.IsZero() {
		return domain.Round{}, domain.NewValidationError("round.scheduledAt", "must be set")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Round{}, fmt.Errorf("postgres begin add round: %w", err)
	}
	if err := lockOrganization(ctx, tx, organization); err != nil {
		return domain.Round{}, rollback(tx, err)
	}
	var maximum sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		"SELECT MAX(id) FROM rounds WHERE organization = $1", organization,
	).Scan(&maximum); err != nil {
		return domain.Round{}, rollback(tx, mapDatabaseError("read maximum round", err))
	}
	next := 0
	if maximum.Valid {
		next = int(maximum.Int64) + 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO rounds (organization, id, scheduled_date, done)
		VALUES ($1, $2, $3, false)`, organization, next, scheduledAt.UTC()); err != nil {
		return domain.Round{}, rollback(tx, mapDatabaseError("add round", err))
	}
	if err := tx.Commit(); err != nil {
		return domain.Round{}, mapDatabaseError("commit add round", err)
	}
	return domain.Round{Organization: organization, ID: next, ScheduledAt: scheduledAt.UTC(), Pairs: []domain.Pair{}}, nil
}

// RescheduleRound changes a scheduled time and clears any scheduler claim.
func (r *Repository) RescheduleRound(ctx context.Context, organization string, roundID int, scheduledAt time.Time) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("round.organization", "must not be empty")
	}
	if roundID < 0 {
		return domain.NewValidationError("round.id", "must be non-negative")
	}
	if scheduledAt.IsZero() {
		return domain.NewValidationError("round.scheduledAt", "must be set")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE rounds SET scheduled_date = $1, processing_started_at = NULL, processing_token = NULL
		WHERE organization = $2 AND id = $3`, scheduledAt.UTC(), organization, roundID)
	if err != nil {
		return mapDatabaseError("reschedule round", err)
	}
	return requireChanged(result, "round", fmt.Sprintf("%s/%d", organization, roundID))
}

// DeleteRound removes a round and its pairs, compacts every subsequent round
// and cascading pair ID, then rebuilds member histories in one transaction.
func (r *Repository) DeleteRound(ctx context.Context, organization string, roundID int) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("round.organization", "must not be empty")
	}
	if roundID < 0 {
		return domain.NewValidationError("round.id", "must be non-negative")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres begin delete round: %w", err)
	}
	if err := lockOrganization(ctx, tx, organization); err != nil {
		return rollback(tx, err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, processing_token
		FROM rounds
		WHERE organization = $1 AND id >= $2
		ORDER BY id
		FOR UPDATE`, organization, roundID)
	if err != nil {
		return rollback(tx, mapDatabaseError("lock affected rounds", err))
	}
	ids := make([]int, 0)
	targetFound := false
	claimedRoundID := -1
	for rows.Next() {
		var id int
		var token sql.NullString
		if err := rows.Scan(&id, &token); err != nil {
			rows.Close()
			return rollback(tx, mapDatabaseError("scan affected round", err))
		}
		ids = append(ids, id)
		if id == roundID {
			targetFound = true
		}
		if token.Valid && claimedRoundID < 0 {
			claimedRoundID = id
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return rollback(tx, mapDatabaseError("scan affected rounds", err))
	}
	if err := rows.Close(); err != nil {
		return rollback(tx, fmt.Errorf("postgres close affected rounds: %w", err))
	}
	if !targetFound {
		return rollback(tx, domain.NewNotFoundError("round", fmt.Sprintf("%s/%d", organization, roundID)))
	}
	if claimedRoundID >= 0 {
		return rollback(tx, domain.NewConflictError(
			"delete round",
			fmt.Sprintf("round %d is owned by an active scheduler claim", claimedRoundID),
			nil,
		))
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM pairs WHERE organization = $1 AND round = $2", organization, roundID,
	); err != nil {
		return rollback(tx, mapDatabaseError("delete round pairs", err))
	}
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM rounds WHERE organization = $1 AND id = $2", organization, roundID,
	); err != nil {
		return rollback(tx, mapDatabaseError("delete round", err))
	}
	for _, id := range ids {
		if id == roundID {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE rounds SET id = $1 WHERE organization = $2 AND id = $3`,
			id-1, organization, id,
		); err != nil {
			return rollback(tx, mapDatabaseError("compact round IDs", err))
		}
	}
	if err := reconstructOrganizationHistoryTx(ctx, tx, organization); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return mapDatabaseError("commit delete round", err)
	}
	return nil
}

// ClaimDueRounds atomically leases due unfinished rounds. Rows claimed after
// staleBefore are skipped, while row locks and SKIP LOCKED prevent concurrent
// schedulers from returning the same work.
func (r *Repository) ClaimDueRounds(ctx context.Context, now, staleBefore time.Time, limit int) ([]domain.Round, error) {
	if now.IsZero() || staleBefore.IsZero() {
		return nil, domain.NewValidationError("round.cutoff", "times must be set")
	}
	if staleBefore.After(now) {
		return nil, domain.NewValidationError("round.cutoff", "stale cutoff must not be after now")
	}
	if limit <= 0 {
		return nil, domain.NewValidationError("round.limit", "must be positive")
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH due AS (
			SELECT organization, id
			FROM rounds
			WHERE done = false
			  AND scheduled_date <= $1
			  AND (processing_started_at IS NULL OR processing_started_at <= $2)
			ORDER BY scheduled_date, organization, id
			FOR UPDATE SKIP LOCKED
			LIMIT $3
		)
		UPDATE rounds AS r
		SET processing_started_at = $1, processing_token = gen_random_uuid()::text
		FROM due
		WHERE r.organization = due.organization AND r.id = due.id
		RETURNING r.organization, r.id, r.scheduled_date, r.done, r.processing_token`,
		now.UTC(), staleBefore.UTC(), limit)
	if err != nil {
		return nil, mapDatabaseError("claim due rounds", err)
	}
	defer rows.Close()
	rounds, err := scanClaimedRounds(rows, "claim due rounds")
	if err != nil {
		return nil, err
	}
	sort.Slice(rounds, func(i, j int) bool {
		if rounds[i].ScheduledAt.Equal(rounds[j].ScheduledAt) {
			if rounds[i].Organization == rounds[j].Organization {
				return rounds[i].ID < rounds[j].ID
			}
			return rounds[i].Organization < rounds[j].Organization
		}
		return rounds[i].ScheduledAt.Before(rounds[j].ScheduledAt)
	})
	return rounds, nil
}

// FindDueRounds leases due rounds with the default retry interval. The cutoff
// is supplied by the caller's clock to keep scheduler behavior deterministic.
func (r *Repository) FindDueRounds(ctx context.Context, cutoff time.Time) ([]domain.Round, error) {
	return r.ClaimDueRounds(ctx, cutoff, cutoff.Add(-defaultClaimLease), 100)
}

// ReleaseRoundClaim makes a failed unfinished round immediately eligible for
// retry only when claimToken still owns its scheduler lease. Completed rounds
// are never reopened.
func (r *Repository) ReleaseRoundClaim(ctx context.Context, organization string, roundID int, claimToken string) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("round.organization", "must not be empty")
	}
	if roundID < 0 {
		return domain.NewValidationError("round.id", "must be non-negative")
	}
	if strings.TrimSpace(claimToken) == "" {
		return domain.NewValidationError("round.claimToken", "must not be empty")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE rounds SET processing_started_at = NULL, processing_token = NULL
		WHERE organization = $1 AND id = $2 AND done = false AND processing_token = $3`,
		organization, roundID, claimToken)
	if err != nil {
		return mapDatabaseError("release round claim", err)
	}
	return requireClaimOwned(result, "release round claim")
}

func scanRounds(rows *sql.Rows, operation string) ([]domain.Round, error) {
	rounds := make([]domain.Round, 0)
	for rows.Next() {
		var round domain.Round
		if err := rows.Scan(&round.Organization, &round.ID, &round.ScheduledAt, &round.Done); err != nil {
			return nil, mapDatabaseError(operation, err)
		}
		round.ScheduledAt = round.ScheduledAt.UTC()
		round.Pairs = []domain.Pair{}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(operation, err)
	}
	return rounds, nil
}

func scanClaimedRounds(rows *sql.Rows, operation string) ([]domain.Round, error) {
	rounds := make([]domain.Round, 0)
	for rows.Next() {
		var round domain.Round
		if err := rows.Scan(&round.Organization, &round.ID, &round.ScheduledAt, &round.Done, &round.ClaimToken); err != nil {
			return nil, mapDatabaseError(operation, err)
		}
		round.ScheduledAt = round.ScheduledAt.UTC()
		round.Pairs = []domain.Pair{}
		rounds = append(rounds, round)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(operation, err)
	}
	return rounds, nil
}

func lockOrganization(ctx context.Context, tx *sql.Tx, organization string) error {
	var name string
	if err := tx.QueryRowContext(ctx,
		"SELECT name FROM organizations WHERE name = $1 FOR UPDATE", organization,
	).Scan(&name); err != nil {
		if err == sql.ErrNoRows {
			return domain.NewNotFoundError("organization", organization)
		}
		return mapDatabaseError("lock organization", err)
	}
	return nil
}

func requireChanged(result sql.Result, resource, identifier string) error {
	return requireRowsAffected(result, resource, domain.NewNotFoundError(resource, identifier))
}

func requireClaimOwned(result sql.Result, operation string) error {
	return requireRowsAffected(result, operation, domain.NewConflictError(operation, "scheduler claim is no longer owned", nil))
}

// requireRowsAffected returns zeroRows when the statement changed no rows.
func requireRowsAffected(result sql.Result, operation string, zeroRows error) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("postgres inspect %s update: %w", operation, err)
	}
	if changed == 0 {
		return zeroRows
	}
	return nil
}
