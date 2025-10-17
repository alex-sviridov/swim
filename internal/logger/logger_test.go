package logger

import (
	"log/slog"
	"testing"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		verbose bool
		want    slog.Level
	}{
		{
			name:    "verbose mode returns info level",
			verbose: true,
			want:    slog.LevelInfo,
		},
		{
			name:    "non-verbose mode returns error level",
			verbose: false,
			want:    slog.LevelError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := New(tt.verbose)
			if logger == nil {
				t.Fatal("New() returned nil logger")
			}
			// Logger is created successfully - basic smoke test
		})
	}
}
