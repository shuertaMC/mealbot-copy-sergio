// Package logging configures the process-wide log/slog logger.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// ParseLevel converts a level name (debug, info, warn, error) into a
// slog.Level. Unknown names yield slog.LevelInfo.
func ParseLevel(name string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New builds a text logger writing to w at the given level.
func New(w io.Writer, level string) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)}))
}

// Setup installs a stderr logger as the slog default and returns it.
func Setup(level string) *slog.Logger {
	logger := New(os.Stderr, level)
	slog.SetDefault(logger)
	return logger
}
