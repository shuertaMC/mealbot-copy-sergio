package postgrestest

import "testing"

func TestValidateDatabaseURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "exact", raw: "postgresql://tester@localhost/test", want: "test"},
		{name: "prefix segment", raw: "postgresql://tester@localhost/test_mealbot", want: "test_mealbot"},
		{name: "middle segment", raw: "postgresql://tester@localhost/mealbot_test_123", want: "mealbot_test_123"},
		{name: "hyphen segment", raw: "postgresql://tester@localhost/mealbot-test", want: "mealbot-test"},
		{name: "socket query", raw: "postgresql:///mealbot_test?host=%2Fvar%2Frun%2Fpostgresql", want: "mealbot_test"},
		{name: "case insensitive", raw: "postgres://tester@localhost/MealBot_TEST?sslmode=disable", want: "MealBot_TEST"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ValidateDatabaseURL(test.raw)
			if err != nil || got != test.want {
				t.Fatalf("ValidateDatabaseURL(%q) = %q, %v, want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func TestValidateDatabaseURLRejectsUnsafeURLs(t *testing.T) {
	t.Parallel()
	tests := []string{
		"",
		"localhost/mealbot_test",
		"mysql://tester@localhost/mealbot_test",
		"postgresql://tester@localhost/postgres",
		"postgresql://tester@localhost/latest",
		"postgresql://tester@localhost/contest",
		"postgresql://tester@localhost/testimonials",
		"postgresql://tester@localhost/mealbot_testimonials",
		"postgresql://tester@localhost/mealbot_test123",
		"postgresql://tester@localhost/mealbottest",
		"postgresql://tester@localhost/mealbot_test?dbname=production",
		"postgresql://tester@localhost/mealbot_test%ZZ",
	}
	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			if got, err := ValidateDatabaseURL(raw); err == nil {
				t.Fatalf("ValidateDatabaseURL(%q) = %q, want error", raw, got)
			}
		})
	}
}
