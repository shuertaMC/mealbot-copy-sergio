package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

// ListPairs returns stored member IDs grouped in ordered rounds.
func (r *Repository) ListPairs(ctx context.Context, organization string) ([]domain.Round, error) {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return nil, domain.NewValidationError("pair.organization", "must not be empty")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.organization, r.id, r.scheduled_date, r.done,
		       p.id1, p.id2, p.extra_id
		FROM rounds r
		LEFT JOIN pairs p ON p.organization = r.organization AND p.round = r.id
		WHERE r.organization = $1 AND r.done = true
		ORDER BY r.id, p.id1, p.id2`, organization)
	if err != nil {
		return nil, mapDatabaseError("list pairs", err)
	}
	defer rows.Close()
	rounds := make([]domain.Round, 0)
	index := make(map[int]int)
	for rows.Next() {
		var round domain.Round
		var id1, id2, extra sql.NullString
		if err := rows.Scan(
			&round.Organization, &round.ID, &round.ScheduledAt, &round.Done,
			&id1, &id2, &extra,
		); err != nil {
			return nil, mapDatabaseError("list pairs", err)
		}
		position, exists := index[round.ID]
		if !exists {
			round.ScheduledAt = round.ScheduledAt.UTC()
			round.Pairs = make([]domain.Pair, 0)
			position = len(rounds)
			index[round.ID] = position
			rounds = append(rounds, round)
		}
		if !id1.Valid || !id2.Valid {
			if id1.Valid || id2.Valid || extra.Valid {
				return nil, domain.NewIntegrityError("list pairs", fmt.Sprintf("organization %q round %d contains incomplete pair data", organization, round.ID), nil)
			}
			continue
		}
		pair := domain.Pair{ID1: id1.String, ID2: id2.String}
		if extra.Valid {
			pair.ExtraID = extra.String
		}
		rounds[position].Pairs = append(rounds[position].Pairs, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError("list pairs", err)
	}
	return rounds, nil
}

// ListHistoricalPairs resolves pair IDs using all members, including inactive
// ones. Missing references return a typed integrity error rather than a
// zero-value member.
func (r *Repository) ListHistoricalPairs(ctx context.Context, organization string) ([]domain.HistoricalRound, error) {
	rounds, err := r.ListPairs(ctx, organization)
	if err != nil {
		return nil, err
	}
	members, err := r.ListMembers(ctx, organization, false)
	if err != nil {
		return nil, err
	}
	byEmail := make(map[string]domain.Member, len(members))
	for _, member := range members {
		byEmail[member.Email] = member
	}
	history := make([]domain.HistoricalRound, 0, len(rounds))
	for _, round := range rounds {
		group := domain.HistoricalRound{Round: round, Pairs: make([]domain.HistoricalPair, 0, len(round.Pairs))}
		for _, pair := range round.Pairs {
			member1, ok := byEmail[pair.ID1]
			if !ok {
				return nil, missingPairMember(organization, round.ID, pair.ID1)
			}
			member2, ok := byEmail[pair.ID2]
			if !ok {
				return nil, missingPairMember(organization, round.ID, pair.ID2)
			}
			resolved := domain.HistoricalPair{Pair: pair, Member1: member1, Member2: member2}
			if pair.ExtraID != "" {
				extra, ok := byEmail[pair.ExtraID]
				if !ok {
					return nil, missingPairMember(organization, round.ID, pair.ExtraID)
				}
				resolved.ExtraMember = &extra
			}
			group.Pairs = append(group.Pairs, resolved)
		}
		history = append(history, group)
	}
	return history, nil
}

// PersistPairings atomically inserts all pairs/triples, updates every
// relationship symmetrically, and marks the round complete when claimToken
// still owns its scheduler lease. A completed or superseded claim returns a
// conflict so scheduler retries cannot duplicate persistence.
func (r *Repository) PersistPairings(ctx context.Context, organization string, roundID int, claimToken string, pairs []domain.Pair) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("pair.organization", "must not be empty")
	}
	if roundID < 0 {
		return domain.NewValidationError("round.id", "must be non-negative")
	}
	if strings.TrimSpace(claimToken) == "" {
		return domain.NewValidationError("round.claimToken", "must not be empty")
	}
	seenMembers := make(map[string]struct{})
	seenPairs := make(map[domain.Pair]struct{})
	for _, pair := range pairs {
		if err := pair.Validate(); err != nil {
			return err
		}
		if _, duplicate := seenPairs[pair]; duplicate {
			return domain.NewConflictError("persist pairings", "duplicate pair", nil)
		}
		seenPairs[pair] = struct{}{}
		for _, email := range pair.Members() {
			if _, duplicate := seenMembers[email]; duplicate {
				return domain.NewConflictError("persist pairings", "member appears more than once", nil)
			}
			seenMembers[email] = struct{}{}
		}
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres begin persist pairings: %w", err)
	}
	var done bool
	if err := tx.QueryRowContext(ctx, `
		SELECT done FROM rounds
		WHERE organization = $1 AND id = $2 AND processing_token = $3
		FOR UPDATE`,
		organization, roundID, claimToken,
	).Scan(&done); err != nil {
		if err == sql.ErrNoRows {
			return rollback(tx, domain.NewConflictError("persist pairings", "scheduler claim is no longer owned", nil))
		}
		return rollback(tx, mapDatabaseError("lock pairing round", err))
	}
	if done {
		return rollback(tx, domain.NewConflictError("persist pairings", "round is already complete", nil))
	}
	var stored int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pairs WHERE organization = $1 AND round = $2`,
		organization, roundID,
	).Scan(&stored); err != nil {
		return rollback(tx, mapDatabaseError("check existing pairs", err))
	}
	if stored != 0 {
		return rollback(tx, domain.NewIntegrityError("persist pairings", "unfinished round already contains pairs", nil))
	}

	members, err := loadMembersForUpdate(ctx, tx, organization)
	if err != nil {
		return rollback(tx, err)
	}
	for email := range seenMembers {
		if _, ok := members[email]; !ok {
			return rollback(tx, missingPairMember(organization, roundID, email))
		}
	}
	for _, pair := range pairs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO pairs (organization, id1, id2, extra_id, round)
			VALUES ($1, $2, $3, $4, $5)`,
			organization, pair.ID1, pair.ID2, nullableExtraID(pair.ExtraID), roundID,
		); err != nil {
			return rollback(tx, mapDatabaseError("insert pair", err))
		}
		ids := pair.Members()
		for leftIndex, leftID := range ids {
			for _, rightID := range ids[leftIndex+1:] {
				left := members[leftID]
				right := members[rightID]
				left.LastRoundWith[rightID] = roundID
				right.LastRoundWith[leftID] = roundID
				members[leftID] = left
				members[rightID] = right
			}
		}
	}
	for _, email := range sortedDomainMemberKeys(members) {
		if _, changed := seenMembers[email]; !changed {
			continue
		}
		history, err := encodeHistory(members[email].LastRoundWith)
		if err != nil {
			return rollback(tx, domain.NewIntegrityError("encode member history", "invalid history", err))
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE members SET last_round_with = $1
			WHERE organization = $2 AND email = $3`, history, organization, email)
		if err != nil {
			return rollback(tx, mapDatabaseError("update pairing history", err))
		}
		if err := requireChanged(result, "member", email); err != nil {
			return rollback(tx, err)
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE rounds
		SET done = true, processing_started_at = NULL, processing_token = NULL
		WHERE organization = $1 AND id = $2 AND done = false AND processing_token = $3`,
		organization, roundID, claimToken)
	if err != nil {
		return rollback(tx, mapDatabaseError("complete round", err))
	}
	if err := requireClaimOwned(result, "complete pairing round"); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return mapDatabaseError("commit pairings", err)
	}
	return nil
}

// ReconstructHistory rebuilds pairing history for every organization. Each
// organization commits independently in one transaction, making repeated runs
// idempotent while keeping failures observable.
func (r *Repository) ReconstructHistory(ctx context.Context) error {
	organizations, err := r.ListAllOrganizations(ctx)
	if err != nil {
		return err
	}
	for _, organization := range organizations {
		if err := r.ReconstructOrganizationHistory(ctx, organization.Name); err != nil {
			return err
		}
	}
	return nil
}

// ReconstructOrganizationHistory rebuilds all active and inactive member
// histories from stored pairs and triples in one transaction.
func (r *Repository) ReconstructOrganizationHistory(ctx context.Context, organization string) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("organization.name", "must not be empty")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres begin reconstruct history: %w", err)
	}
	if err := lockOrganization(ctx, tx, organization); err != nil {
		return rollback(tx, err)
	}
	if err := reconstructOrganizationHistoryTx(ctx, tx, organization); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return mapDatabaseError("commit reconstruct history", err)
	}
	return nil
}

func reconstructOrganizationHistoryTx(ctx context.Context, tx *sql.Tx, organization string) error {
	members, err := loadMembersForUpdate(ctx, tx, organization)
	if err != nil {
		return err
	}
	for email, member := range members {
		member.LastRoundWith = make(map[string]int, len(members)-1)
		for other := range members {
			if other != email {
				member.LastRoundWith[other] = -1
			}
		}
		members[email] = member
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT round, id1, id2, extra_id
		FROM pairs WHERE organization = $1 ORDER BY round, id1, id2 FOR UPDATE`, organization)
	if err != nil {
		return mapDatabaseError("load history pairs", err)
	}
	type storedPair struct {
		round int
		pair  domain.Pair
	}
	stored := make([]storedPair, 0)
	for rows.Next() {
		var item storedPair
		var extra sql.NullString
		if err := rows.Scan(&item.round, &item.pair.ID1, &item.pair.ID2, &extra); err != nil {
			rows.Close()
			return mapDatabaseError("scan history pair", err)
		}
		if extra.Valid {
			item.pair.ExtraID = extra.String
		}
		stored = append(stored, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return mapDatabaseError("scan history pairs", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("postgres close history pairs: %w", err)
	}
	for _, item := range stored {
		ids := item.pair.Members()
		for _, email := range ids {
			if _, ok := members[email]; !ok {
				return missingPairMember(organization, item.round, email)
			}
		}
		for leftIndex, leftID := range ids {
			for _, rightID := range ids[leftIndex+1:] {
				left := members[leftID]
				right := members[rightID]
				left.LastRoundWith[rightID] = item.round
				right.LastRoundWith[leftID] = item.round
				members[leftID] = left
				members[rightID] = right
			}
		}
	}
	for _, email := range sortedDomainMemberKeys(members) {
		history, err := encodeHistory(members[email].LastRoundWith)
		if err != nil {
			return domain.NewIntegrityError("encode reconstructed history", "invalid history", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE members SET last_round_with = $1
			WHERE organization = $2 AND email = $3`, history, organization, email,
		); err != nil {
			return mapDatabaseError("save reconstructed history", err)
		}
	}
	return nil
}

func loadMembersForUpdate(ctx context.Context, tx *sql.Tx, organization string) (map[string]domain.Member, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT organization, email, name, metadata, active, last_round_with
		FROM members WHERE organization = $1 FOR UPDATE`, organization)
	if err != nil {
		return nil, mapDatabaseError("lock history members", err)
	}
	membersSlice, scanErr := scanMembers(rows, "lock history members")
	closeErr := rows.Close()
	if scanErr != nil {
		return nil, scanErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("postgres close history members: %w", closeErr)
	}
	members := make(map[string]domain.Member, len(membersSlice))
	for _, member := range membersSlice {
		members[member.Email] = member
	}
	return members, nil
}

func sortedDomainMemberKeys(members map[string]domain.Member) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nullableExtraID(extraID string) any {
	if extraID == "" {
		return nil
	}
	return extraID
}

func missingPairMember(organization string, round int, email string) error {
	return domain.NewIntegrityError(
		"pair history",
		fmt.Sprintf("organization %q round %d references missing member %q", organization, round, email),
		nil,
	)
}
