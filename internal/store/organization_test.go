package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateOrganizationPreservesLegacyValidationBoundary(t *testing.T) {
	t.Parallel()
	var store *Store
	if err := store.CreateOrganization(context.Background(), "", "admin@example.com"); !errors.Is(err, ErrOrganizationNameRequired) {
		t.Fatalf("empty name error = %v", err)
	}
	for _, test := range []struct {
		name  string
		admin string
	}{
		{name: " ", admin: "admin@example.com"},
		{name: "org", admin: " \t"},
		{name: "org", admin: ""},
	} {
		if err := store.CreateOrganization(context.Background(), test.name, test.admin); !errors.Is(err, ErrNilDatabase) {
			t.Errorf("CreateOrganization(%q, %q) error = %v, want %v", test.name, test.admin, err, ErrNilDatabase)
		}
	}
}

func TestCreateOrganizationPreservesDatabaseErrorTextForHTTPCompatibility(t *testing.T) {
	db := openDriverDB(&driverState{failSQL: "INSERT INTO organizations"})
	t.Cleanup(func() { _ = db.Close() })
	store, err := New(db)
	if err != nil {
		t.Fatal(err)
	}

	err = store.CreateOrganization(context.Background(), "alpha", "owner@example.com")
	if err == nil || err.Error() != "injected execution failure" {
		t.Fatalf("CreateOrganization() error = %v, want exact database error text", err)
	}
}
