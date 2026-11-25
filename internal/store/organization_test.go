package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateOrganizationValidatesNameAndAdministratorBeforeDatabaseAccess(t *testing.T) {
	t.Parallel()
	var store *Store
	if err := store.CreateOrganization(context.Background(), " ", "admin@example.com"); !errors.Is(err, ErrOrganizationNameRequired) {
		t.Fatalf("empty name error = %v", err)
	}
	if err := store.CreateOrganization(context.Background(), "org", " \t"); !errors.Is(err, ErrOrganizationAdminRequired) {
		t.Fatalf("empty admin error = %v", err)
	}
}
