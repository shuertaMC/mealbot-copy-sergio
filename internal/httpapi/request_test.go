package httpapi

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/johnamadeo/mealbot/internal/domain"
)

func TestQueryValueRequiresExactlyOneNonEmptyValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
		want string
		ok   bool
	}{
		{name: "valid", url: "/?org=%20alpha%20", want: "alpha", ok: true},
		{name: "missing", url: "/", ok: false},
		{name: "repeated", url: "/?org=alpha&org=beta", ok: false},
		{name: "empty", url: "/?org=", ok: false},
		{name: "blank", url: "/?org=%20", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := QueryValue(httptest.NewRequest("GET", test.url, nil), "org")
			if test.ok {
				if err != nil || got != test.want {
					t.Fatalf("QueryValue() = %q, %v; want %q, nil", got, err, test.want)
				}
				return
			}
			if !errors.Is(err, domain.ErrValidation) {
				t.Fatalf("error = %v; want validation", err)
			}
		})
	}
}

func TestQueryValuesPreservesKeyOrder(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest("GET", "/?round=4&org=alpha", nil)
	values, err := QueryValues(request, "org", "round")
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[0] != "alpha" || values[1] != "4" {
		t.Fatalf("values = %#v", values)
	}
}
