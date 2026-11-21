// Package store contains the raw-SQL data access used by the HTTP handlers and
// the pairing scheduler.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/johnamadeo/mealbot/internal/db"
)

const (
	// TimestampFormat : Postgres timestamp string template patterns can be found in https://www.postgresql.org/docs/8.1/functions-formatting.html
	TimestampFormat = "YYYY-MM-DD HH24:MI:ssZ"
)

// Store wraps a db.Querier (typically a *pgxpool.Pool).
type Store struct {
	q db.Querier
}

// New creates a Store.
func New(q db.Querier) *Store {
	return &Store{q: q}
}

// Member :
type Member struct {
	Organization  string
	Email         string `json:"email"`
	Name          string `json:"name"`
	Metadata      map[string]string
	LastRoundWith map[string]int
}

// MemberResponse : Data structure for representing a member
type MemberResponse map[string]string

// PairedMembers : Data structure for a pairing
type PairedMembers struct {
	Member1     Member `json:"member1"`
	Member2     Member `json:"member2"`
	ExtraMember Member `json:"extraMember"`
}

// ---------------------------------------------------------------------------
// Organizations

// GetOrganizations returns the names of the organizations managed by admin.
func (s *Store) GetOrganizations(ctx context.Context, admin string) ([]string, error) {
	rows, err := s.q.Query(ctx, "SELECT name FROM organizations WHERE admin = $1", admin)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	organizations := []string{}
	for rows.Next() {
		var organization string
		if err := rows.Scan(&organization); err != nil {
			return []string{}, err
		}
		organizations = append(organizations, organization)
	}
	if err := rows.Err(); err != nil {
		return []string{}, err
	}

	return organizations, nil
}

// GetAllOrganizations returns the names of every organization.
func (s *Store) GetAllOrganizations(ctx context.Context) ([]string, error) {
	rows, err := s.q.Query(ctx, "SELECT name FROM organizations")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	organizations := []string{}
	for rows.Next() {
		var organization string
		if err := rows.Scan(&organization); err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return organizations, nil
}

// CreateOrganization inserts a new organization.
func (s *Store) CreateOrganization(ctx context.Context, name string, admin string) error {
	if name == "" {
		return errors.New("Organization name cannot be an empty string")
	}

	_, err := s.q.Exec(
		ctx,
		"INSERT INTO organizations (name, admin) VALUES ($1, $2)",
		name,
		admin,
	)
	return err
}

// GetCrossMatchTrait returns the cross match trait of an organization, or ""
// if it is unset or the organization does not exist.
func (s *Store) GetCrossMatchTrait(ctx context.Context, orgname string) (string, error) {
	var trait *string
	err := s.q.QueryRow(
		ctx,
		"SELECT cross_match_trait FROM organizations WHERE name = $1",
		orgname,
	).Scan(&trait)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if trait == nil {
		return "", nil
	}
	return *trait, nil
}

// SetCrossMatchTrait sets the cross match trait of an organization.
func (s *Store) SetCrossMatchTrait(ctx context.Context, orgname string, crossMatchTrait string) error {
	_, err := s.q.Exec(
		ctx,
		"UPDATE organizations SET cross_match_trait = $1 WHERE name = $2",
		crossMatchTrait,
		orgname,
	)
	return err
}

// ---------------------------------------------------------------------------
// Rounds

// GetRounds returns the scheduled dates (formatted by TimestampFormat) of all
// rounds of an organization ordered by round id.
func (s *Store) GetRounds(ctx context.Context, orgname string) ([]string, error) {
	rows, err := s.q.Query(
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

// AddRound schedules a new round with the next free id.
func (s *Store) AddRound(ctx context.Context, orgname string, roundDate string) error {
	var maxID *int
	err := s.q.QueryRow(
		ctx,
		"SELECT MAX(id) FROM rounds WHERE organization = $1",
		orgname,
	).Scan(&maxID)
	if err != nil {
		return err
	}

	maxRoundID := -1
	if maxID != nil {
		maxRoundID = *maxID
	}

	_, err = s.q.Exec(
		ctx,
		"INSERT INTO rounds (organization, id, scheduled_date, done) VALUES ($1, $2, $3, $4)",
		orgname,
		maxRoundID+1,
		roundDate,
		false,
	)
	return err
}

// RemoveRound deletes a round and shifts the ids of all later rounds down.
func (s *Store) RemoveRound(ctx context.Context, orgname string, roundID int) error {
	_, err := s.q.Exec(
		ctx,
		"DELETE FROM rounds WHERE organization = $1 AND id = $2",
		orgname,
		roundID,
	)
	if err != nil {
		return err
	}

	for {
		tag, err := s.q.Exec(
			ctx,
			"UPDATE rounds SET id = $1 WHERE organization = $2 AND id = $3",
			roundID,
			orgname,
			roundID+1,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			break
		}

		roundID++
	}

	return nil
}

// RescheduleRound changes the date of a round.
func (s *Store) RescheduleRound(ctx context.Context, orgname string, roundDate string, roundID int) error {
	_, err := s.q.Exec(
		ctx,
		"UPDATE rounds SET scheduled_date = $1 WHERE organization = $2 AND id = $3",
		roundDate,
		orgname,
		roundID,
	)
	return err
}

// PendingRound identifies a round that is due and not yet done.
type PendingRound struct {
	Organization string
	ID           int
}

// GetPendingRounds returns the rounds whose scheduled date has passed and that
// have not been run yet.
func (s *Store) GetPendingRounds(ctx context.Context) ([]PendingRound, error) {
	rows, err := s.q.Query(
		ctx,
		"SELECT organization, id FROM rounds WHERE scheduled_date < now() AT TIME ZONE 'utc' AND done = false",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	pending := []PendingRound{}
	for rows.Next() {
		var p PendingRound
		if err := rows.Scan(&p.Organization, &p.ID); err != nil {
			return nil, err
		}
		pending = append(pending, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return pending, nil
}

// MarkRoundDone flags a round as done.
func (s *Store) MarkRoundDone(ctx context.Context, orgname string, roundID int) error {
	_, err := s.q.Exec(
		ctx,
		"UPDATE rounds SET done = $1 WHERE organization = $2 AND id = $3",
		true,
		orgname,
		roundID,
	)
	return err
}

// ---------------------------------------------------------------------------
// Pairs

// InsertPair stores one pairing of a round.
func (s *Store) InsertPair(ctx context.Context, orgname, id1, id2, extraID string, round int) error {
	columns := "(organization, id1, id2, extraId, round)"
	placeholder := "($1, $2, $3, $4, $5)"

	_, err := s.q.Exec(
		ctx,
		fmt.Sprintf("INSERT INTO pairs %s VALUES %s", columns, placeholder),
		orgname,
		id1,
		id2,
		extraID,
		round,
	)
	return err
}

// GetPairs returns all the pairings of an organization, separated by rounds.
func (s *Store) GetPairs(ctx context.Context, orgname string) ([][]PairedMembers, error) {
	roundPairs := [][]PairedMembers{}

	members, err := s.getActiveMembersInPairFormat(ctx, orgname)
	if err != nil {
		return roundPairs, err
	}
	membersMap := map[string]Member{}
	for _, member := range members {
		membersMap[member.Email] = member
	}

	for round := 0; ; round++ {
		pairs, err := s.getRoundPairs(ctx, orgname, round, membersMap)
		if err != nil {
			return roundPairs, err
		}
		if len(pairs) == 0 {
			break
		}
		roundPairs = append(roundPairs, pairs)
	}

	return roundPairs, nil
}

func (s *Store) getRoundPairs(ctx context.Context, orgname string, round int, membersMap map[string]Member) ([]PairedMembers, error) {
	pairRows, err := s.q.Query(
		ctx,
		"SELECT id1, id2, extraId FROM pairs WHERE organization = $1 AND round = $2",
		orgname,
		round,
	)
	if err != nil {
		return nil, err
	}
	defer pairRows.Close()

	pairs := []PairedMembers{}
	for pairRows.Next() {
		var id1, id2 string
		var extraID *string
		if err := pairRows.Scan(&id1, &id2, &extraID); err != nil {
			return nil, err
		}

		if extraID != nil {
			pairs = append(pairs, PairedMembers{
				Member1:     membersMap[id1],
				Member2:     membersMap[id2],
				ExtraMember: membersMap[*extraID],
			})
		} else {
			pairs = append(pairs, PairedMembers{
				Member1: membersMap[id1],
				Member2: membersMap[id2],
			})
		}
	}
	if err := pairRows.Err(); err != nil {
		return nil, err
	}
	return pairs, nil
}

// ---------------------------------------------------------------------------
// Members

// GetMembers returns the members of an organization ordered by name.
func (s *Store) GetMembers(ctx context.Context, orgname string, onlyActive bool) ([]Member, error) {
	members := []Member{}

	rows, err := s.q.Query(
		ctx,
		"SELECT organization, name, email, metadata, last_round_with, active FROM members WHERE organization = $1 ORDER BY name",
		orgname,
	)
	if err != nil {
		return members, err
	}
	defer rows.Close()

	for rows.Next() {
		var organization, name, email string
		var metadataRaw, lastRoundWithRaw []byte
		var active bool
		err := rows.Scan(&organization, &name, &email, &metadataRaw, &lastRoundWithRaw, &active)
		if err != nil {
			return members, err
		}

		if onlyActive && !active {
			continue
		}

		bytes, err := db.JSONB(metadataRaw).MarshalJSON()
		if err != nil {
			return members, err
		}

		var metadata map[string]string
		if err := json.Unmarshal(bytes, &metadata); err != nil {
			return members, err
		}

		bytes, err = db.JSONB(lastRoundWithRaw).MarshalJSON()
		if err != nil {
			return members, err
		}

		var lastRoundWith map[string]int
		if err := json.Unmarshal(bytes, &lastRoundWith); err != nil {
			return members, err
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
		return members, err
	}

	return members, nil
}

// GetActiveMembersAsMaps returns the active members flattened into
// name/email/metadata maps.
func (s *Store) GetActiveMembersAsMaps(ctx context.Context, orgname string) ([]MemberResponse, error) {
	members, err := s.GetMembers(ctx, orgname, true)
	if err != nil {
		return []MemberResponse{}, err
	}

	mapMembers := []MemberResponse{}
	for _, member := range members {
		mapMember := member.Metadata
		if mapMember == nil {
			mapMember = map[string]string{}
		}
		mapMember["name"] = member.Name
		mapMember["email"] = member.Email
		mapMembers = append(mapMembers, mapMember)
	}

	return mapMembers, nil
}

func (s *Store) getActiveMembersInPairFormat(ctx context.Context, orgname string) ([]Member, error) {
	members, err := s.GetMembers(ctx, orgname, true)
	if err != nil {
		return []Member{}, err
	}

	pairMembers := []Member{}
	for _, member := range members {
		pairMembers = append(pairMembers, Member{
			Name:  member.Name,
			Email: member.Email,
		})
	}

	return pairMembers, nil
}

// UpdateLastRoundWith overwrites the last_round_with data of a member.
func (s *Store) UpdateLastRoundWith(ctx context.Context, orgname, email string, lastRoundWith map[string]int) error {
	bytes, err := json.Marshal(lastRoundWith)
	if err != nil {
		return err
	}

	_, err = s.q.Exec(
		ctx,
		"UPDATE members SET last_round_with = $1 WHERE organization = $2 AND email = $3",
		db.JSONB(bytes).Arg(),
		orgname,
		email,
	)
	return err
}

// SaveMembers syncs the members of an organization with newMembers: existing
// members are updated, missing ones are deactivated (never deleted) and new
// ones are inserted.
//
// TODO: Use transactions. The current implementation is brittle since it does
// not roll back if insertion of multiple members fails halfway.
func (s *Store) SaveMembers(ctx context.Context, orgname string, newMembers []Member) error {
	newMembersMap := map[string]Member{}
	for _, member := range newMembers {
		newMembersMap[member.Email] = member
	}

	membersMap := map[string]Member{}
	members, err := s.GetMembers(ctx, orgname, false)
	if err != nil {
		return err
	}

	for _, member := range members {
		membersMap[member.Email] = member
	}

	// remove existing members that are not in new list
	for email := range membersMap {
		if _, ok := newMembersMap[email]; !ok {
			delete(membersMap, email)
		}
	}

	// update fields of existing member (if existing member changes name or metadata)
	for email := range membersMap {
		if _, ok := newMembersMap[email]; ok {
			membersMap[email] = Member{
				Organization:  membersMap[email].Organization,
				Email:         membersMap[email].Email,         // cannot be updated by user
				LastRoundWith: membersMap[email].LastRoundWith, // update later
				Name:          newMembersMap[email].Name,
				Metadata:      newMembersMap[email].Metadata,
			}
		}
	}

	// update pair counts of existing members with new members
	for email, member := range membersMap {
		if member.LastRoundWith == nil {
			member.LastRoundWith = map[string]int{}
		}
		for newEmail := range newMembersMap {
			// if new email is not existing member
			if _, ok := membersMap[newEmail]; !ok {
				member.LastRoundWith[newEmail] = -1
			}
		}

		membersMap[email] = member
	}

	// save new member
	for email := range newMembersMap {
		if _, ok := membersMap[email]; !ok {
			lastRoundWith := map[string]int{}

			for otherEmail := range newMembersMap {
				if otherEmail == email {
					continue
				}
				lastRoundWith[otherEmail] = -1
			}

			membersMap[email] = Member{
				Organization:  orgname,
				Name:          newMembersMap[email].Name,
				Email:         newMembersMap[email].Email,
				Metadata:      newMembersMap[email].Metadata,
				LastRoundWith: lastRoundWith,
			}
		}
	}

	existingMemberEmails := map[string]bool{}
	for _, member := range members {
		existingMemberEmails[member.Email] = true
	}

	for _, member := range membersMap {
		metadataBytes, err := json.Marshal(member.Metadata)
		if err != nil {
			return err
		}

		lastRoundWithBytes, err := json.Marshal(member.LastRoundWith)
		if err != nil {
			return err
		}

		if _, ok := existingMemberEmails[member.Email]; !ok {
			// Add new member
			columns := "(organization, email, name, metadata, last_round_with, active)"
			placeholders := "($1, $2, $3, $4, $5, $6)"
			_, err = s.q.Exec(
				ctx,
				fmt.Sprintf(
					"INSERT INTO members %s VALUES %s",
					columns,
					placeholders,
				),
				member.Organization,
				member.Email,
				member.Name,
				db.JSONB(metadataBytes).Arg(),
				db.JSONB(lastRoundWithBytes).Arg(),
				true,
			)
			if err != nil {
				return err
			}
		} else {
			// Update existing member
			_, err = s.q.Exec(
				ctx,
				"UPDATE members SET name = $1, metadata = $2, last_round_with = $3, active = $4 WHERE organization = $5 AND email = $6",
				member.Name,
				db.JSONB(metadataBytes).Arg(),
				db.JSONB(lastRoundWithBytes).Arg(),
				true,
				orgname,
				member.Email,
			)
			if err != nil {
				return err
			}
		}
	}

	// Deactivate member (note we don't delete member from the DB)
	for email := range existingMemberEmails {
		if _, ok := membersMap[email]; !ok {
			_, err = s.q.Exec(
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
	}

	return nil
}

// IsDuplicateKeyErr reports whether err is a duplicate key violation.
func IsDuplicateKeyErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), db.DuplicateKeyErr)
}
