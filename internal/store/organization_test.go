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
