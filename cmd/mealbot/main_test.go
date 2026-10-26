package main

import (
	"context"
	"strings"
	"testing"

	"github.com/johnamadeo/mealbot/internal/config"
	"github.com/johnamadeo/mealbot/internal/logging"
)

func TestRunRejectsUnknownArguments(t *testing.T) {
	err := run(context.Background(), []string{"bogus"}, config.Config{}, logging.Discard())
	if err == nil || !strings.Contains(err.Error(), "'bogus' not recognized") {
		t.Errorf("unexpected error: %v", err)
	}

	err = run(context.Background(), []string{"pair", "extra"}, config.Config{}, logging.Discard())
	if err == nil || !strings.Contains(err.Error(), "not recognized") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestModesRequireDatabaseURL(t *testing.T) {
	for _, args := range [][]string{nil, {"pair"}, {"migrate"}} {
		err := run(context.Background(), args, config.Config{Port: "0"}, logging.Discard())
		if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
			t.Errorf("args %v: expected DATABASE_URL error, got %v", args, err)
		}
	}
}
