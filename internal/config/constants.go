package config

import (
	"os"
	"strconv"
	"time"
)

// Redis queue keys
const (
	ProvisionQueueKey    = "vmmanager:provision"
	DecommissionQueueKey = "vmmanager:decommission"
)

// Redis cache keys
const (
	ServerCachePrefix = "vmmanager:servers:"
)

// Server statuses for VMManager
const (
	StatusProvisioning = "provisioning"
	StatusRunning      = "running"
	StatusStopping     = "stopping"
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

// Retry configuration for cache read operations
const (
	CacheReadRetryAttempts = 3
	CacheReadRetryTimeout  = 2 * time.Second
)

// GetProvisionRateLimitDuration returns the rate limit duration for provision operations
// Reads from PROVISION_RATE_LIMIT_SECONDS environment variable, defaults to 15 seconds
func GetProvisionRateLimitDuration() time.Duration {
	if seconds := os.Getenv("PROVISION_RATE_LIMIT_SECONDS"); seconds != "" {
		if val, err := strconv.Atoi(seconds); err == nil && val > 0 {
			return time.Duration(val) * time.Second
		}
	}
	return 15 * time.Second // default
}

// GetDecommissionRateLimitDuration returns the rate limit duration for decommission operations
// Reads from DECOMMISSION_RATE_LIMIT_SECONDS environment variable, defaults to 15 seconds
func GetDecommissionRateLimitDuration() time.Duration {
	if seconds := os.Getenv("DECOMMISSION_RATE_LIMIT_SECONDS"); seconds != "" {
		if val, err := strconv.Atoi(seconds); err == nil && val > 0 {
			return time.Duration(val) * time.Second
		}
	}
	return 15 * time.Second // default
}
