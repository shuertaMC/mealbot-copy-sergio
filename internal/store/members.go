package store

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/johnamadeo/mealbot/internal/server"
)

// Member is a person taking part in an organization's rounds.
type Member struct {
	Organization  string
	Email         string `json:"email"`
	Name          string `json:"name"`
	Metadata      map[string]string
	LastRoundWith map[string]int
}

// GetMembers returns the members of an organization ordered by name,
// optionally restricted to active members.
func (s *Store) GetMembers(ctx context.Context, orgname string, onlyActive bool) ([]Member, error) {
	return getMembers(ctx, s.db, orgname, onlyActive)
}

func getMembers(ctx context.Context, q DBTX, orgname string, onlyActive bool) ([]Member, error) {
	rows, err := q.QueryContext(
		ctx,
		"SELECT organization, name, email, metadata, last_round_with, active FROM members WHERE organization = $1 ORDER BY name",
		orgname,
	)
	if err != nil {
		return []Member{}, err
	}
	defer rows.Close()

	members := []Member{}
	for rows.Next() {
		var organization, name, email string
		var metadataJSON, lastRoundWithJSON server.JSONB
		var active bool
		if err := rows.Scan(&organization, &name, &email, &metadataJSON, &lastRoundWithJSON, &active); err != nil {
			return []Member{}, err
		}

		if onlyActive && !active {
			continue
		}

		metadata := map[string]string{}
		if !metadataJSON.IsNull() {
			if err := json.Unmarshal(metadataJSON, &metadata); err != nil {
				return []Member{}, err
			}
		}

		lastRoundWith := map[string]int{}
		if !lastRoundWithJSON.IsNull() {
			if err := json.Unmarshal(lastRoundWithJSON, &lastRoundWith); err != nil {
				return []Member{}, err
			}
		}

		members = append(members, Member{
			Organization:  organization,
			Name:          name,
			Email:         email,
			Metadata:      metadata,
			LastRoundWith: lastRoundWith,
		})
	}
	if err := rows.Err(); err != nil {
		return []Member{}, err
	}

	return members, nil
}

// SaveMembers replaces the member list of an organization in one transaction:
//   - members missing from newMembers are deactivated (never deleted),
//   - members present in both keep their pairing history but take the new
//     name and metadata (and are reactivated),
//   - brand new members are inserted with no history.
func (s *Store) SaveMembers(ctx context.Context, orgname string, newMembers []Member) error {
	return s.inTx(ctx, func(tx DBTX) error {
		return saveMembers(ctx, tx, orgname, newMembers)
	})
}

func saveMembers(ctx context.Context, q DBTX, orgname string, newMembers []Member) error {
	newMembersMap := map[string]Member{}
	for _, member := range newMembers {
		newMembersMap[member.Email] = member
	}

	existing, err := getMembers(ctx, q, orgname, false)
	if err != nil {
		return err
	}
	existingByEmail := map[string]Member{}
	for _, member := range existing {
		existingByEmail[member.Email] = member
	}

	// members that stay: update fields (name or metadata may change)
	merged := map[string]Member{}
	for email, old := range existingByEmail {
		replacement, ok := newMembersMap[email]
		if !ok {
			continue
		}

		lastRoundWith := old.LastRoundWith
		if lastRoundWith == nil {
			lastRoundWith = map[string]int{}
		}
		merged[email] = Member{
			Organization:  old.Organization,
			Email:         old.Email, // cannot be updated by user
			Name:          replacement.Name,
			Metadata:      replacement.Metadata,
			LastRoundWith: lastRoundWith,
		}
	}

	// members that stay have never been paired with the brand new members
	for _, member := range merged {
		for newEmail := range newMembersMap {
			if _, stays := merged[newEmail]; !stays {
				member.LastRoundWith[newEmail] = -1
			}
		}
	}

	// brand new members have never been paired with anybody
	for email, member := range newMembersMap {
		if _, ok := merged[email]; ok {
			continue
		}

		lastRoundWith := map[string]int{}
		for otherEmail := range newMembersMap {
			if otherEmail != email {
				lastRoundWith[otherEmail] = -1
			}
		}

		merged[email] = Member{
			Organization:  orgname,
			Email:         member.Email,
			Name:          member.Name,
			Metadata:      member.Metadata,
			LastRoundWith: lastRoundWith,
		}
	}

	emails := make([]string, 0, len(merged))
	for email := range merged {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	for _, email := range emails {
		member := merged[email]
		if member.Metadata == nil {
			member.Metadata = map[string]string{}
		}

		metadataBytes, err := json.Marshal(member.Metadata)
		if err != nil {
			return err
		}
		lastRoundWithBytes, err := json.Marshal(member.LastRoundWith)
		if err != nil {
			return err
		}

		if _, exists := existingByEmail[email]; !exists {
			_, err = q.ExecContext(
				ctx,
				"INSERT INTO members (organization, email, name, metadata, last_round_with, active) VALUES ($1, $2, $3, $4, $5, $6)",
				member.Organization,
				member.Email,
				member.Name,
				server.JSONB(metadataBytes),
				server.JSONB(lastRoundWithBytes),
				true,
			)
		} else {
			_, err = q.ExecContext(
				ctx,
				"UPDATE members SET name = $1, metadata = $2, last_round_with = $3, active = $4 WHERE organization = $5 AND email = $6",
				member.Name,
				server.JSONB(metadataBytes),
				server.JSONB(lastRoundWithBytes),
				true,
				orgname,
				member.Email,
			)
		}
		if err != nil {
			return err
		}
	}

	// deactivate members that were left out (note we don't delete them from the DB)
	for email := range existingByEmail {
		if _, ok := merged[email]; ok {
			continue
		}
		_, err := q.ExecContext(
			ctx,
			"UPDATE members SET active = $1 WHERE organization = $2 AND email = $3",
			false,
			orgname,
			email,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpdateLastRoundWith stores the pairing history of each member (keyed by email).
func (s *Store) UpdateLastRoundWith(ctx context.Context, orgname string, history map[string]map[string]int) error {
	return s.inTx(ctx, func(tx DBTX) error {
		return updateLastRoundWith(ctx, tx, orgname, history)
	})
}

func updateLastRoundWith(ctx context.Context, q DBTX, orgname string, history map[string]map[string]int) error {
	emails := make([]string, 0, len(history))
	for email := range history {
		emails = append(emails, email)
	}
	sort.Strings(emails)

	for _, email := range emails {
		bytes, err := json.Marshal(history[email])
		if err != nil {
			return err
		}

		_, err = q.ExecContext(
			ctx,
			"UPDATE members SET last_round_with = $1 WHERE organization = $2 AND email = $3",
			server.JSONB(bytes),
			orgname,
			email,
		)
		if err != nil {
			return err
		}
	}
	return nil
}
