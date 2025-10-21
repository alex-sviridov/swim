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
			name:     "StateRunning",
			got:      StateRunning,
			expected: "running",
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
		{
			name:     "MaxRetryAttempts",
			got:      MaxRetryAttempts,
			expected: 5,
		},
		{
			name:     "InitialRetryDelay",
			got:      InitialRetryDelay,
			expected: 5 * time.Second,
		},
		{
			name:     "MaxRetryDelay",
			got:      MaxRetryDelay,
			expected: 60 * time.Second,
		},
		{
			name:     "RetryBackoffMultiple",
			got:      RetryBackoffMultiple,
			expected: 2,
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
