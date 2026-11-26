package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

// ListMembers returns members in stable name/email order. When activeOnly is
// false, inactive historical members are included.
func (r *Repository) ListMembers(ctx context.Context, organization string, activeOnly bool) ([]domain.Member, error) {
	organization = strings.TrimSpace(organization)
	if err := domain.RequireNonEmpty("member.organization", organization); err != nil {
		return nil, err
	}
	query := `SELECT organization, email, name, metadata, active, last_round_with
		FROM members WHERE organization = $1`
	if activeOnly {
		query += " AND active = true"
	}
	query += " ORDER BY name, email"
	rows, err := r.db.QueryContext(ctx, query, organization)
	if err != nil {
		return nil, mapDatabaseError("list members", err)
	}
	defer rows.Close()
	return scanMembers(rows, "list members")
}

// ReconcileMembers atomically makes imported members the active set. Existing
// histories are preserved, present members are updated/reactivated, absent
// members are deactivated, and every known relationship is made symmetric.
func (r *Repository) ReconcileMembers(ctx context.Context, organization string, imported []domain.Member) ([]domain.Member, error) {
	organization = strings.TrimSpace(organization)
	if err := domain.RequireNonEmpty("member.organization", organization); err != nil {
		return nil, err
	}
	if len(imported) == 0 {
		return nil, domain.NewValidationError("members", "must not be empty")
	}
	incoming := make(map[string]domain.Member, len(imported))
	for _, member := range imported {
		member.Organization = strings.TrimSpace(member.Organization)
		if member.Organization == "" {
			member.Organization = organization
		}
		member.Active = true
		if member.Metadata == nil {
			member.Metadata = map[string]string{}
		}
		if member.LastRoundWith == nil {
			member.LastRoundWith = map[string]int{}
		}
		if member.Organization != organization {
			return nil, domain.NewValidationError("member.organization", "must match reconciliation organization")
		}
		if err := member.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := incoming[member.Email]; duplicate {
			return nil, domain.NewConflictError("reconcile members", "duplicate member email", nil)
		}
		incoming[member.Email] = member.Clone()
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("postgres begin reconcile members: %w", err)
	}
	if err := lockOrganization(ctx, tx, organization); err != nil {
		return nil, rollback(tx, err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT organization, email, name, metadata, active, last_round_with
		FROM members WHERE organization = $1 FOR UPDATE`, organization)
	if err != nil {
		return nil, rollback(tx, mapDatabaseError("lock members", err))
	}
	existingMembers, scanErr := scanMembers(rows, "lock members")
	closeErr := rows.Close()
	if scanErr != nil {
		return nil, rollback(tx, scanErr)
	}
	if closeErr != nil {
		return nil, rollback(tx, fmt.Errorf("postgres close member rows: %w", closeErr))
	}

	all := make(map[string]domain.Member, len(existingMembers)+len(incoming))
	for _, member := range existingMembers {
		member.Active = false
		all[member.Email] = member
	}
	for email, member := range incoming {
		if old, exists := all[email]; exists {
			member.LastRoundWith = mergeHistory(old.LastRoundWith, member.LastRoundWith)
		}
		member.Active = true
		all[email] = member
	}
	makeHistoriesSymmetric(all)

	for _, email := range sortedMemberKeys(all) {
		member := all[email]
		metadata, err := encodeStringMap(member.Metadata)
		if err != nil {
			return nil, rollback(tx, domain.NewIntegrityError("encode member metadata", "invalid metadata", err))
		}
		history, err := encodeHistory(member.LastRoundWith)
		if err != nil {
			return nil, rollback(tx, domain.NewIntegrityError("encode member history", "invalid history", err))
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO members (organization, email, name, metadata, active, last_round_with)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (organization, email) DO UPDATE SET
				name = EXCLUDED.name,
				metadata = EXCLUDED.metadata,
				active = EXCLUDED.active,
				last_round_with = EXCLUDED.last_round_with`,
			organization, member.Email, member.Name, metadata, member.Active, history,
		); err != nil {
			return nil, rollback(tx, mapDatabaseError("save reconciled member", err))
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, mapDatabaseError("commit reconcile members", err)
	}

	active := make([]domain.Member, 0, len(incoming))
	for _, member := range all {
		if member.Active {
			active = append(active, member.Clone())
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].Name == active[j].Name {
			return active[i].Email < active[j].Email
		}
		return active[i].Name < active[j].Name
	})
	return active, nil
}

func scanMembers(rows *sql.Rows, operation string) ([]domain.Member, error) {
	members := make([]domain.Member, 0)
	for rows.Next() {
		var member domain.Member
		var metadataJSON, historyJSON []byte
		if err := rows.Scan(
			&member.Organization, &member.Email, &member.Name,
			&metadataJSON, &member.Active, &historyJSON,
		); err != nil {
			return nil, mapDatabaseError(operation, err)
		}
		metadata, err := decodeStringMap(metadataJSON)
		if err != nil {
			return nil, domain.NewIntegrityError(operation, "member metadata is not a string object", err)
		}
		history, err := decodeHistory(historyJSON)
		if err != nil {
			return nil, domain.NewIntegrityError(operation, "member history is not an integer object", err)
		}
		member.Metadata = metadata
		member.LastRoundWith = history
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError(operation, err)
	}
	return members, nil
}

func decodeStringMap(value []byte) (map[string]string, error) {
	result := make(map[string]string)
	if len(value) == 0 || string(value) == "null" {
		return result, nil
	}
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = make(map[string]string)
	}
	return result, nil
}

func decodeHistory(value []byte) (map[string]int, error) {
	result := make(map[string]int)
	if len(value) == 0 || string(value) == "null" {
		return result, nil
	}
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = make(map[string]int)
	}
	return result, nil
}

func encodeStringMap(value map[string]string) ([]byte, error) {
	if value == nil {
		value = map[string]string{}
	}
	return json.Marshal(value)
}

func encodeHistory(value map[string]int) ([]byte, error) {
	if value == nil {
		value = map[string]int{}
	}
	return json.Marshal(value)
}

func mergeHistory(current, incoming map[string]int) map[string]int {
	merged := make(map[string]int, len(current)+len(incoming))
	for partner, round := range current {
		merged[partner] = round
	}
	for partner, round := range incoming {
		if old, exists := merged[partner]; !exists || round > old {
			merged[partner] = round
		}
	}
	return merged
}

func makeHistoriesSymmetric(members map[string]domain.Member) {
	keys := sortedMemberKeys(members)
	for _, email := range keys {
		member := members[email]
		if member.LastRoundWith == nil {
			member.LastRoundWith = make(map[string]int)
		}
		delete(member.LastRoundWith, email)
		members[email] = member
	}
	for i, leftEmail := range keys {
		for _, rightEmail := range keys[i+1:] {
			left := members[leftEmail]
			right := members[rightEmail]
			leftRound, leftOK := left.LastRoundWith[rightEmail]
			rightRound, rightOK := right.LastRoundWith[leftEmail]
			round := -1
			if leftOK && leftRound > round {
				round = leftRound
			}
			if rightOK && rightRound > round {
				round = rightRound
			}
			left.LastRoundWith[rightEmail] = round
			right.LastRoundWith[leftEmail] = round
			members[leftEmail] = left
			members[rightEmail] = right
		}
	}
}

func sortedMemberKeys(members map[string]domain.Member) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
