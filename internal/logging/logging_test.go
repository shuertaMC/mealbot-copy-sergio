package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug, "DEBUG": slog.LevelDebug, "warn": slog.LevelWarn,
		"error": slog.LevelError, "info": slog.LevelInfo, "": slog.LevelInfo, "bogus": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewFiltersByLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf, "warn")
	l.Info("quiet")
	l.Warn("loud")
	if strings.Contains(buf.String(), "quiet") || !strings.Contains(buf.String(), "loud") {
		t.Errorf("unexpected output: %q", buf.String())
	}
}
