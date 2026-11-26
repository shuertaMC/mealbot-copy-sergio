package postgres

import (
	"context"
	"database/sql"
	"strings"

	"github.com/johnamadeo/mealbot/internal/domain"
)

// ListOrganizations returns organizations administered by admin in name order.
func (r *Repository) ListOrganizations(ctx context.Context, admin string) ([]domain.Organization, error) {
	admin = strings.TrimSpace(admin)
	if admin == "" {
		return nil, domain.NewValidationError("organization.admin", "must not be empty")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, admin, cross_match_trait
		FROM organizations WHERE admin = $1 ORDER BY name`, admin)
	if err != nil {
		return nil, mapDatabaseError("list organizations", err)
	}
	defer rows.Close()

	organizations := make([]domain.Organization, 0)
	for rows.Next() {
		organization, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError("list organizations", err)
	}
	return organizations, nil
}

// ListAllOrganizations returns every organization in stable name order. It is
// used by administrative history reconstruction rather than HTTP filtering.
func (r *Repository) ListAllOrganizations(ctx context.Context) ([]domain.Organization, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, admin, cross_match_trait FROM organizations ORDER BY name`)
	if err != nil {
		return nil, mapDatabaseError("list all organizations", err)
	}
	defer rows.Close()
	organizations := make([]domain.Organization, 0)
	for rows.Next() {
		organization, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	if err := rows.Err(); err != nil {
		return nil, mapDatabaseError("list all organizations", err)
	}
	return organizations, nil
}

// CreateOrganization validates and inserts organization, returning a typed
// conflict error when its name already exists.
func (r *Repository) CreateOrganization(ctx context.Context, organization domain.Organization) error {
	if err := organization.Validate(); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO organizations (name, admin, cross_match_trait)
		VALUES ($1, $2, $3)`,
		organization.Name, organization.Admin, nullableString(organization.CrossMatchTrait))
	return mapDatabaseError("create organization", err)
}

// CrossMatchTrait returns the organization's optional metadata trait. A nil
// pointer distinguishes an unset trait from a missing organization.
func (r *Repository) CrossMatchTrait(ctx context.Context, organization string) (*string, error) {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return nil, domain.NewValidationError("organization.name", "must not be empty")
	}
	var trait sql.NullString
	if err := r.db.QueryRowContext(ctx,
		"SELECT cross_match_trait FROM organizations WHERE name = $1", organization,
	).Scan(&trait); err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.NewNotFoundError("organization", organization)
		}
		return nil, mapDatabaseError("read cross-match trait", err)
	}
	if !trait.Valid {
		return nil, nil
	}
	value := trait.String
	return &value, nil
}

// UpdateCrossMatchTrait sets or clears an organization's configured trait.
func (r *Repository) UpdateCrossMatchTrait(ctx context.Context, organization string, trait *string) error {
	organization = strings.TrimSpace(organization)
	if organization == "" {
		return domain.NewValidationError("organization.name", "must not be empty")
	}
	if trait != nil {
		trimmed := strings.TrimSpace(*trait)
		if trimmed == "" {
			return domain.NewValidationError("organization.crossMatchTrait", "must not be empty when set")
		}
		trait = &trimmed
	}
	result, err := r.db.ExecContext(ctx,
		"UPDATE organizations SET cross_match_trait = $1 WHERE name = $2",
		nullableString(trait), organization)
	if err != nil {
		return mapDatabaseError("update cross-match trait", err)
	}
	return requireChanged(result, "organization", organization)
}

type rowScanner interface {
	Scan(...any) error
}

func scanOrganization(scanner rowScanner) (domain.Organization, error) {
	var organization domain.Organization
	var trait sql.NullString
	if err := scanner.Scan(&organization.Name, &organization.Admin, &trait); err != nil {
		return domain.Organization{}, mapDatabaseError("scan organization", err)
	}
	if trait.Valid {
		organization.CrossMatchTrait = &trait.String
	}
	return organization, nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
