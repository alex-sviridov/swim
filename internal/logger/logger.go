package logger

import (
	"log/slog"
	"os"
)

// New creates a new slog logger with appropriate log level
// If verbose is true, logs at Info level and above
// If verbose is false, logs only Error level and above
func New(verbose bool) *slog.Logger {
	level := slog.LevelError
	if verbose {
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	return slog.New(handler)
}
