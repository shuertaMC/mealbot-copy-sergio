package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var ErrOrganizationNameRequired = errors.New("organization name is required")

// Organization is the persistence representation of an organization. A nil
// CrossMatchTrait represents SQL NULL rather than an empty configured trait.
type Organization struct {
	Name            string
	Admin           string
	CrossMatchTrait *string
}

// CreateOrganization validates and inserts an organization.
func (s *Store) CreateOrganization(ctx context.Context, name, admin string) error {
	if name == "" {
		return ErrOrganizationNameRequired
	}
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(opCtx,
		`INSERT INTO organizations (name, admin) VALUES ($1, $2)`,
		name, admin,
	); err != nil {
		return fmt.Errorf("create organization: %w", err)
	}
	return nil
}

// OrganizationsByAdmin returns organization names in SQL-defined order.
func (s *Store) OrganizationsByAdmin(ctx context.Context, admin string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	rows, err := s.db.QueryContext(opCtx, `
SELECT name
FROM organizations
WHERE admin = $1
ORDER BY name
`, admin)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	organizations := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		organizations = append(organizations, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organizations: %w", err)
	}
	return organizations, nil
}

// CrossMatchTrait reads a nullable organization trait. A nil result means the
// database value is SQL NULL or the organization does not exist; an empty
// non-nil string remains distinguishable.
func (s *Store) CrossMatchTrait(ctx context.Context, organization string) (*string, error) {
	if s == nil || s.db == nil {
		return nil, ErrNilDatabase
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	var trait sql.NullString
	if err := s.db.QueryRowContext(opCtx,
		`SELECT cross_match_trait FROM organizations WHERE name = $1`,
		organization,
	).Scan(&trait); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read cross-match trait: %w", err)
	}
	if !trait.Valid {
		return nil, nil
	}
	value := trait.String
	return &value, nil
}

// SetCrossMatchTrait writes a nullable trait. Passing nil restores SQL NULL.
func (s *Store) SetCrossMatchTrait(ctx context.Context, organization string, trait *string) error {
	if s == nil || s.db == nil {
		return ErrNilDatabase
	}
	var value any
	if trait != nil {
		value = *trait
	}
	opCtx, cancel := s.operationContext(ctx)
	defer cancel()
	if _, err := s.db.ExecContext(opCtx, `
UPDATE organizations
SET cross_match_trait = $1
WHERE name = $2
`, value, organization); err != nil {
		return fmt.Errorf("set cross-match trait: %w", err)
	}
	return nil
}
