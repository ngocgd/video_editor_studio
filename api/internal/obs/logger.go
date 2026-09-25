// Package obs provides the process-wide structured logging setup.
package obs

import (
	"log/slog"
	"os"
	"strings"

	"loomtale/api/internal/obs/scrub"
)

// NewLogger returns a JSON slog.Logger reading its level from the given
// string ("debug", "info", "warn", "error"; defaults to "info").
func NewLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       lvl,
		ReplaceAttr: scrub.ReplaceAttr,
	})
	return slog.New(handler)
}
