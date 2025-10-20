package config

import (
	"testing"
	"time"
)

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{
			name:     "ServerCachePrefix",
			got:      ServerCachePrefix,
			expected: "swim:server:",
		},
		{
			name:     "StateBooted",
			got:      StateBooted,
			expected: "booted",
		},
		{
			name:     "StateDeletedError",
			got:      StateDeletedError,
			expected: "deleted-error",
		},
		{
			name:     "ServerCacheTTL",
			got:      ServerCacheTTL,
			expected: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("got %v, want %v", tt.got, tt.expected)
			}
		})
	}
}
