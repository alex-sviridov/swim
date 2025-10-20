package config

import "time"

// Redis cache keys
const (
	ServerCachePrefix = "swim:server:"
)

// Server states
const (
	StateBooted       = "booted"
	StateDeletedError = "deleted-error"
)

// Cache TTL
const (
	ServerCacheTTL = 24 * time.Hour
)
