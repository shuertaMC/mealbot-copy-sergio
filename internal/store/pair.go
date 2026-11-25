package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/johnamadeo/mealbot/internal/pairing"
)

// HistoricalPair is one stored pair joined to its persisted member records.
// ExtraID distinguishes SQL NULL from a present empty string; ExtraMember is
// nil when the optional ID is NULL, empty, or no longer resolves to a member.
type HistoricalPair struct {
	Member1     Member
	Member2     Member
	ExtraID     *string
	ExtraMember *Member
}

// PairHistory returns rounds starting at zero and stops at the first missing
// round, retaining the legacy grouping contract.
func (s *Store) PairHistory(ctx context.Context, organization string) ([][]HistoricalPair, error) {
	if s == nil || s.db == nil {
		return nil, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(opCtx, pairHistoryQuery, organization)
	if err != nil {
		return nil, fmt.Errorf("list pair history: %w", err)
	}
	defer rows.Close()

	byRound := make(map[int][]HistoricalPair)
	for rows.Next() {
		round, pair, err := scanHistoricalPair(rows)
		if err != nil {
			return nil, err
		}
		byRound[round] = append(byRound[round], pair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pair history: %w", err)
	}

	grouped := make([][]HistoricalPair, 0)
	for round := 0; ; round++ {
		pairs, exists := byRound[round]
		if !exists || len(pairs) == 0 {
			break
		}
		grouped = append(grouped, pairs)
	}
	return grouped, nil
}

const pairHistoryQuery = `
SELECT p.round, p.extraid,
       m1.organization, m1.email, m1.name, m1.metadata, m1.last_round_with, m1.active,
       m2.organization, m2.email, m2.name, m2.metadata, m2.last_round_with, m2.active,
       m3.organization, m3.email, m3.name, m3.metadata, m3.last_round_with, m3.active
FROM pairs AS p
JOIN members AS m1
  ON m1.organization = p.organization AND m1.email = p.id1
JOIN members AS m2
  ON m2.organization = p.organization AND m2.email = p.id2
LEFT JOIN members AS m3
  ON m3.organization = p.organization AND m3.email = p.extraid
WHERE p.organization = $1
ORDER BY p.round, p.id1, p.id2, p.extraid NULLS FIRST
`

type rowScanner interface {
	Scan(...any) error
}

func scanHistoricalPair(row rowScanner) (int, HistoricalPair, error) {
	var round int
	var extraID sql.NullString
	var first, second Member
	var firstMetadata, firstHistory, secondMetadata, secondHistory []byte
	var extraOrganization, extraEmail, extraName sql.NullString
	var extraMetadata, extraHistory []byte
	var extraActive sql.NullBool
	if err := row.Scan(
		&round, &extraID,
		&first.Organization, &first.Email, &first.Name, &firstMetadata, &firstHistory, &first.Active,
		&second.Organization, &second.Email, &second.Name, &secondMetadata, &secondHistory, &second.Active,
		&extraOrganization, &extraEmail, &extraName, &extraMetadata, &extraHistory, &extraActive,
	); err != nil {
		return 0, HistoricalPair{}, fmt.Errorf("scan pair history: %w", err)
	}
	if err := decodeMemberJSON(firstMetadata, firstHistory, &first); err != nil {
		return 0, HistoricalPair{}, fmt.Errorf("decode first pair member: %w", err)
	}
	if err := decodeMemberJSON(secondMetadata, secondHistory, &second); err != nil {
		return 0, HistoricalPair{}, fmt.Errorf("decode second pair member: %w", err)
	}
	pair := HistoricalPair{Member1: first, Member2: second}
	if extraID.Valid {
		value := extraID.String
		pair.ExtraID = &value
	}
	if extraEmail.Valid {
		extra := Member{
			Organization: extraOrganization.String,
			Email:        extraEmail.String,
			Name:         extraName.String,
			Active:       extraActive.Bool,
		}
		if err := decodeMemberJSON(extraMetadata, extraHistory, &extra); err != nil {
			return 0, HistoricalPair{}, fmt.Errorf("decode extra pair member: %w", err)
		}
		pair.ExtraMember = &extra
	}
	return round, pair, nil
}

// SaveRound implements pairing.RoundStore. Pair insertion, every member
// history update, and round completion either commit together or all roll back.
func (s *Store) SaveRound(ctx context.Context, organization string, round pairing.Round, members pairing.MembersMap) error {
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	return s.WithinTransaction(opCtx, func(tx DBTX) error {
		for _, pair := range orderedRoundPairs(round) {
			if _, err := tx.ExecContext(opCtx, `
INSERT INTO pairs (organization, id1, id2, extraid, round)
VALUES ($1, $2, $3, $4, $5)
`, organization, pair.ID1, pair.ID2, pair.ExtraID, round.Number); err != nil {
				return fmt.Errorf("insert pair %q/%q: %w", pair.ID1, pair.ID2, err)
			}
		}

		for _, memberID := range members.IDs() {
			history, err := json.Marshal(members[memberID].LastRoundWith)
			if err != nil {
				return fmt.Errorf("encode history for member %q: %w", memberID, err)
			}
			result, err := tx.ExecContext(opCtx, `
UPDATE members
SET last_round_with = $1
WHERE organization = $2 AND email = $3
`, string(history), organization, memberID)
			if err != nil {
				return fmt.Errorf("update history for member %q: %w", memberID, err)
			}
			if err := requireOneRow(result, "update history for member "+memberID); err != nil {
				return err
			}
		}

		result, err := tx.ExecContext(opCtx, `
UPDATE rounds
SET done = TRUE
WHERE organization = $1 AND id = $2
`, organization, round.Number)
		if err != nil {
			return fmt.Errorf("mark round done: %w", err)
		}
		if err := requireOneRow(result, "mark round done"); err != nil {
			return err
		}
		return nil
	})
}

func orderedRoundPairs(round pairing.Round) []pairing.Pair {
	pairs := make([]pairing.Pair, 0, len(round.Pairs))
	seen := make(map[pairing.Pair]bool, len(round.Pairs))
	for _, pair := range round.DrawOrderedPairs {
		if round.Pairs[pair] && !seen[pair] {
			pairs = append(pairs, pair)
			seen[pair] = true
		}
	}
	remaining := make([]pairing.Pair, 0, len(round.Pairs))
	for pair, included := range round.Pairs {
		if included && !seen[pair] {
			remaining = append(remaining, pair)
		}
	}
	sort.Slice(remaining, func(i, j int) bool {
		if remaining[i].ID1 != remaining[j].ID1 {
			return remaining[i].ID1 < remaining[j].ID1
		}
		if remaining[i].ID2 != remaining[j].ID2 {
			return remaining[i].ID2 < remaining[j].ID2
		}
		return remaining[i].ExtraID < remaining[j].ExtraID
	})
	return append(pairs, remaining...)
}

func requireOneRow(result sql.Result, action string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: read affected rows: %w", action, err)
	}
	if affected != 1 {
		return fmt.Errorf("%s: affected %d rows, want 1", action, affected)
	}
	return nil
}

// HistoryReconstructionResult provides explicit completion semantics to the
// migrate command while retaining its public success/error behavior.
type HistoryReconstructionResult struct {
	Completed     bool
	Organizations int
	Members       int
	Pairs         int
}

type storedPair struct {
	organization string
	id1          string
	id2          string
	extraID      sql.NullString
	round        int
}

// ReconstructPairHistory derives symmetric member histories from all stored
// pairs. It is safe to repeat: each run deterministically replaces history
// with the same pair-derived values and does not alter other member fields.
func (s *Store) ReconstructPairHistory(ctx context.Context) (HistoryReconstructionResult, error) {
	if s == nil || s.db == nil {
		return HistoryReconstructionResult{}, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	result := HistoryReconstructionResult{}
	err := s.WithinTransaction(opCtx, func(tx DBTX) error {
		memberRows, err := tx.QueryContext(opCtx, `
SELECT organization, email, name, metadata, last_round_with, active
FROM members
ORDER BY organization, email
FOR UPDATE
`)
		if err != nil {
			return fmt.Errorf("load members for history reconstruction: %w", err)
		}
		members, err := scanMembers(memberRows)
		if closeErr := memberRows.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("close reconstruction members: %w", closeErr)
		}
		if err != nil {
			return err
		}

		pairRows, err := tx.QueryContext(opCtx, `
SELECT organization, id1, id2, extraid, round
FROM pairs
ORDER BY organization, round, id1, id2, extraid NULLS FIRST
`)
		if err != nil {
			return fmt.Errorf("load pairs for history reconstruction: %w", err)
		}
		pairs := make([]storedPair, 0)
		for pairRows.Next() {
			var pair storedPair
			if err := pairRows.Scan(&pair.organization, &pair.id1, &pair.id2, &pair.extraID, &pair.round); err != nil {
				pairRows.Close()
				return fmt.Errorf("scan reconstruction pair: %w", err)
			}
			pairs = append(pairs, pair)
		}
		if err := pairRows.Err(); err != nil {
			pairRows.Close()
			return fmt.Errorf("iterate reconstruction pairs: %w", err)
		}
		if err := pairRows.Close(); err != nil {
			return fmt.Errorf("close reconstruction pairs: %w", err)
		}

		byKey := make(map[string]Member, len(members))
		organizations := make(map[string]bool)
		for _, member := range members {
			member.LastRoundWith = make(map[string]int)
			byKey[memberKey(member.Organization, member.Email)] = member
			organizations[member.Organization] = true
		}
		for _, pair := range pairs {
			participants := []string{pair.id1, pair.id2}
			if pair.extraID.Valid && pair.extraID.String != "" {
				participants = append(participants, pair.extraID.String)
			}
			for first := 0; first < len(participants); first++ {
				for second := first + 1; second < len(participants); second++ {
					updateReconstructedHistory(byKey, pair.organization, participants[first], participants[second], pair.round)
				}
			}
		}

		keys := make([]string, 0, len(byKey))
		for key := range byKey {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			member := byKey[key]
			history, err := json.Marshal(member.LastRoundWith)
			if err != nil {
				return fmt.Errorf("encode reconstructed history for %q: %w", member.Email, err)
			}
			updateResult, err := tx.ExecContext(opCtx, `
UPDATE members
SET last_round_with = $1
WHERE organization = $2 AND email = $3
`, string(history), member.Organization, member.Email)
			if err != nil {
				return fmt.Errorf("save reconstructed history for %q: %w", member.Email, err)
			}
			if err := requireOneRow(updateResult, "save reconstructed history for "+member.Email); err != nil {
				return err
			}
		}
		result = HistoryReconstructionResult{
			Completed:     true,
			Organizations: len(organizations),
			Members:       len(members),
			Pairs:         len(pairs),
		}
		return nil
	})
	if err != nil {
		return HistoryReconstructionResult{}, err
	}
	return result, nil
}

func memberKey(organization, email string) string {
	return organization + "\x00" + email
}

func updateReconstructedHistory(members map[string]Member, organization, firstID, secondID string, round int) {
	firstKey, secondKey := memberKey(organization, firstID), memberKey(organization, secondID)
	first, firstExists := members[firstKey]
	second, secondExists := members[secondKey]
	if !firstExists || !secondExists {
		return
	}
	first.LastRoundWith[secondID] = round
	second.LastRoundWith[firstID] = round
	members[firstKey] = first
	members[secondKey] = second
}
