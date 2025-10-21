package config

import "time"

// Redis cache keys
const (
	ServerCachePrefix = "swim:server:"
)

// Server states
const (
	StateRunning      = "running"
	StateDeletedError = "deleted-error"
)

// Cache TTL
const (
	ServerCacheTTL = 24 * time.Hour
)

// Retry configuration for cloud provider operations
const (
	MaxRetryAttempts     = 5
	InitialRetryDelay    = 5 * time.Second
	MaxRetryDelay        = 60 * time.Second
	RetryBackoffMultiple = 2
)
