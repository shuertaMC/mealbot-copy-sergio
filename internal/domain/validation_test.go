package domain

import "testing"

func TestRequireNonEmpty(t *testing.T) {
	t.Parallel()
	if err := RequireNonEmpty("field", " x "); err != nil {
		t.Fatalf("non-blank = %v", err)
	}
	for _, value := range []string{"", "  \t"} {
		if err := RequireNonEmpty("field", value); !IsValidation(err) {
			t.Fatalf("RequireNonEmpty(%q) = %v; want validation error", value, err)
		}
	}
}

func TestRequireRoundID(t *testing.T) {
	t.Parallel()
	for _, id := range []int{0, 7} {
		if err := RequireRoundID(id); err != nil {
			t.Fatalf("RequireRoundID(%d) = %v", id, err)
		}
	}
	if err := RequireRoundID(-1); !IsValidation(err) {
		t.Fatalf("RequireRoundID(-1) = %v; want validation error", err)
	}
}
