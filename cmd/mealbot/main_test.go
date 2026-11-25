package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRunExitContract(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   int
		output string
	}{
		{"success", nil, 0, ""}, {"failure", errors.New("argument 'bad' not recognized"), 1, "argument 'bad' not recognized"},
	}
	for _, test := range tests {
		var stderr bytes.Buffer
		var received []string
		code := run(context.Background(), []string{"bad"}, func(_ context.Context, args []string) error {
			received = append([]string(nil), args...)
			return test.err
		}, &stderr)
		if code != test.code || stderr.String() != test.output || !reflect.DeepEqual(received, []string{"bad"}) {
			t.Errorf("%s: code=%d stderr=%q args=%v", test.name, code, stderr.String(), received)
		}
	}
}
