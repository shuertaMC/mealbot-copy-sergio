package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrEmptyOrganizationName is returned when creating an organization without a name.
var ErrEmptyOrganizationName = errors.New("Organization name cannot be an empty string")

// GetOrganizations returns the names of the organizations managed by admin.
func (s *Store) GetOrganizations(ctx context.Context, admin string) ([]string, error) {
	return queryNames(ctx, s.db, "SELECT name FROM organizations WHERE admin = $1", admin)
}

// ListOrganizations returns the names of all organizations.
func (s *Store) ListOrganizations(ctx context.Context) ([]string, error) {
	return queryNames(ctx, s.db, "SELECT name FROM organizations ORDER BY name")
}

func queryNames(ctx context.Context, q DBTX, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return []string{}, err
	}
	defer rows.Close()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return []string{}, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return []string{}, err
	}
	return names, nil
}

// CreateOrganization inserts a new organization managed by admin.
func (s *Store) CreateOrganization(ctx context.Context, name string, admin string) error {
	if name == "" {
		return ErrEmptyOrganizationName
	}

	_, err := s.db.ExecContext(
		ctx,
		"INSERT INTO organizations (name, admin) VALUES ($1, $2)",
		name,
		admin,
	)
	return err
}

// GetCrossMatchTrait returns the organization's cross match trait, or "" if it
// has none (or the organization does not exist).
func (s *Store) GetCrossMatchTrait(ctx context.Context, orgname string) (string, error) {
	var trait sql.NullString
	err := s.db.QueryRowContext(
		ctx,
		"SELECT cross_match_trait FROM organizations WHERE name = $1",
		orgname,
	).Scan(&trait)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !trait.Valid {
		return "", nil
	}
	return trait.String, nil
}

// SetCrossMatchTrait sets the trait used to cross match members of an organization.
func (s *Store) SetCrossMatchTrait(ctx context.Context, orgname string, crossMatchTrait string) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE organizations SET cross_match_trait = $1 WHERE name = $2",
		crossMatchTrait,
		orgname,
	)
	return err
}
