package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/johnamadeo/mealbot/internal/pairing"
)

// Member is the persistence representation of an organization member.
type Member struct {
	Organization  string
	Email         string
	Name          string
	Metadata      map[string]string
	LastRoundWith map[string]int
	Active        bool
}

// Members returns members in deterministic SQL order. When onlyActive is
// true, inactive rows are filtered by PostgreSQL rather than after scanning.
func (s *Store) Members(ctx context.Context, organization string, onlyActive bool) ([]Member, error) {
	if s == nil || s.db == nil {
		return nil, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	query := `
SELECT organization, email, name, metadata, last_round_with, active
FROM members
WHERE organization = $1
ORDER BY name, email
`
	if onlyActive {
		query = `
SELECT organization, email, name, metadata, last_round_with, active
FROM members
WHERE organization = $1 AND active = TRUE
ORDER BY name, email
`
	}
	rows, err := s.db.QueryContext(opCtx, query, organization)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	members, err := scanMembers(rows)
	if err != nil {
		return nil, err
	}
	return members, nil
}

type memberRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanMembers(rows memberRows) ([]Member, error) {
	members := make([]Member, 0)
	for rows.Next() {
		var member Member
		var metadataJSON, historyJSON []byte
		if err := rows.Scan(
			&member.Organization,
			&member.Email,
			&member.Name,
			&metadataJSON,
			&historyJSON,
			&member.Active,
		); err != nil {
			return nil, fmt.Errorf("scan member: %w", err)
		}
		if err := decodeMemberJSON(metadataJSON, historyJSON, &member); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate members: %w", err)
	}
	return members, nil
}

func decodeMemberJSON(metadataJSON, historyJSON []byte, member *Member) error {
	member.Metadata = make(map[string]string)
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &member.Metadata); err != nil {
			return fmt.Errorf("decode member metadata: %w", err)
		}
	}
	member.LastRoundWith = make(map[string]int)
	if len(historyJSON) > 0 {
		if err := json.Unmarshal(historyJSON, &member.LastRoundWith); err != nil {
			return fmt.Errorf("decode member history: %w", err)
		}
	}
	return nil
}

// SynchronizeMembersCSV parses and plans the complete CSV before opening a
// transaction, then applies that plan atomically.
func (s *Store) SynchronizeMembersCSV(ctx context.Context, organization string, source io.Reader) (MemberSyncPlan, error) {
	members, err := ParseMemberCSV(source)
	if err != nil {
		return MemberSyncPlan{}, err
	}
	plan, err := PlanMemberSynchronization(organization, members)
	if err != nil {
		return MemberSyncPlan{}, err
	}
	if err := s.SynchronizeMembers(ctx, plan); err != nil {
		return MemberSyncPlan{}, err
	}
	return plan, nil
}

// SynchronizeMembers atomically inserts, updates, reactivates, and deactivates
// members. Pair history is retained, and relationships introduced by new rows
// are initialized symmetrically to round -1 for active and inactive members.
func (s *Store) SynchronizeMembers(ctx context.Context, plan MemberSyncPlan) error {
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	validated, err := PlanMemberSynchronization(plan.Organization, plan.Members)
	if err != nil {
		return err
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	return s.WithinTransaction(opCtx, func(tx DBTX) error {
		current, err := loadMembersForUpdate(opCtx, tx, validated.Organization)
		if err != nil {
			return err
		}
		desired := make(map[string]MemberInput, len(validated.Members))
		for _, member := range validated.Members {
			desired[member.Email] = member
		}
		all := make(map[string]Member, len(current)+len(desired))
		for _, member := range current {
			all[member.Email] = member
		}
		for email, input := range desired {
			if _, exists := all[email]; !exists {
				all[email] = Member{
					Organization:  validated.Organization,
					Email:         email,
					Name:          input.Name,
					Metadata:      cloneStringMap(input.Metadata),
					LastRoundWith: make(map[string]int),
					Active:        true,
				}
			}
		}
		initializeSymmetricRelationships(all)

		existing := make(map[string]bool, len(current))
		for _, member := range current {
			existing[member.Email] = true
		}
		emails := make([]string, 0, len(all))
		for email := range all {
			emails = append(emails, email)
		}
		sort.Strings(emails)
		for _, email := range emails {
			member := all[email]
			input, present := desired[email]
			if present {
				member.Name = input.Name
				member.Metadata = cloneStringMap(input.Metadata)
				member.Active = true
			} else {
				member.Active = false
			}
			metadataJSON, err := json.Marshal(member.Metadata)
			if err != nil {
				return fmt.Errorf("encode member metadata: %w", err)
			}
			historyJSON, err := json.Marshal(member.LastRoundWith)
			if err != nil {
				return fmt.Errorf("encode member history: %w", err)
			}
			if !existing[email] {
				if _, err := tx.ExecContext(opCtx, `
INSERT INTO members (organization, email, name, metadata, last_round_with, active)
VALUES ($1, $2, $3, $4, $5, $6)
`, validated.Organization, email, member.Name, string(metadataJSON), string(historyJSON), true); err != nil {
					return fmt.Errorf("insert member: %w", err)
				}
				continue
			}
			if _, err := tx.ExecContext(opCtx, `
UPDATE members
SET name = $1, metadata = $2, last_round_with = $3, active = $4
WHERE organization = $5 AND email = $6
`, member.Name, string(metadataJSON), string(historyJSON), member.Active, validated.Organization, email); err != nil {
				if present {
					return fmt.Errorf("update member: %w", err)
				}
				return fmt.Errorf("deactivate member: %w", err)
			}
		}
		return nil
	})
}

func loadMembersForUpdate(ctx context.Context, tx DBTX, organization string) ([]Member, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT organization, email, name, metadata, last_round_with, active
FROM members
WHERE organization = $1
ORDER BY name, email
FOR UPDATE
`, organization)
	if err != nil {
		return nil, fmt.Errorf("load current members: %w", err)
	}
	defer rows.Close()
	members, err := scanMembers(rows)
	if err != nil {
		return nil, fmt.Errorf("load current members: %w", err)
	}
	return members, nil
}

func initializeSymmetricRelationships(members map[string]Member) {
	emails := make([]string, 0, len(members))
	for email := range members {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	for _, email := range emails {
		member := members[email]
		if member.LastRoundWith == nil {
			member.LastRoundWith = make(map[string]int)
		}
		members[email] = member
	}
	for first := 0; first < len(emails); first++ {
		for second := first + 1; second < len(emails); second++ {
			firstEmail, secondEmail := emails[first], emails[second]
			firstMember, secondMember := members[firstEmail], members[secondEmail]
			firstRound, firstExists := firstMember.LastRoundWith[secondEmail]
			secondRound, secondExists := secondMember.LastRoundWith[firstEmail]
			switch {
			case firstExists && !secondExists:
				secondMember.LastRoundWith[firstEmail] = firstRound
			case !firstExists && secondExists:
				firstMember.LastRoundWith[secondEmail] = secondRound
			case !firstExists && !secondExists:
				firstMember.LastRoundWith[secondEmail] = -1
				secondMember.LastRoundWith[firstEmail] = -1
			}
			members[firstEmail] = firstMember
			members[secondEmail] = secondMember
		}
	}
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// LoadMembers implements pairing.MemberLoader using only active rows and the
// organization's nullable trait.
func (s *Store) LoadMembers(ctx context.Context, organization string) (pairing.MembersMap, error) {
	members, err := s.Members(ctx, organization, true)
	if err != nil {
		return nil, err
	}
	trait, err := s.CrossMatchTrait(ctx, organization)
	if err != nil {
		return nil, err
	}
	traitName := ""
	if trait != nil {
		traitName = *trait
	}
	records := make([]pairing.MemberRecord, 0, len(members))
	for _, member := range members {
		records = append(records, pairing.MemberRecord{
			ID:            member.Email,
			Name:          member.Name,
			Metadata:      cloneStringMap(member.Metadata),
			LastRoundWith: cloneIntMap(member.LastRoundWith),
		})
	}
	return pairing.MembersFromRecords(records, traitName), nil
}

func cloneIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
