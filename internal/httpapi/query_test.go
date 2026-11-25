package httpapi

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestQueryParamCompatibility(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		want    string
		wantErr string
	}{
		{name: "value", target: "/?org=alpha", want: "alpha"},
		{name: "empty", target: "/?org=", want: ""},
		{name: "missing", target: "/", wantErr: "Request query parameters must contain org"},
		{name: "duplicate", target: "/?org=one&org=two", wantErr: "Request query parameters must contain org"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := QueryParam(httptest.NewRequest("GET", test.target, nil), "org")
			if test.wantErr != "" {
				if err == nil || err.Error() != test.wantErr {
					t.Fatalf("error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("value = %q, error = %v", got, err)
			}
		})
	}
}

func TestQueryParamsCompatibility(t *testing.T) {
	req := httptest.NewRequest("GET", "/?org=&round=now", nil)
	got, err := QueryParams(req, []string{"org", "round"})
	if err != nil || !reflect.DeepEqual(got, []string{"", "now"}) {
		t.Fatalf("values = %v, error = %v", got, err)
	}

	req = httptest.NewRequest("GET", "/?org=alpha&round=one&round=two", nil)
	got, err = QueryParams(req, []string{"org", "round"})
	if err == nil || err.Error() != "Request query parameters does not contain round" {
		t.Fatalf("values = %v, error = %v", got, err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("failure values = %#v, want non-nil empty slice", got)
	}
}
