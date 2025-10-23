package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ClientInterface defines the interface for Redis operations
type ClientInterface interface {
	PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error)
	PushPayload(ctx context.Context, queueKey string, payload string) error
	PushServerState(ctx context.Context, cacheKey string, state ServerState, ttl time.Duration) error
	GetServerState(ctx context.Context, cacheKey string) (*ServerState, error)
	GetAllServerStates(ctx context.Context, prefix string) ([]ServerState, error)
	DeleteServerState(ctx context.Context, cacheKey string) error
	TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error)
	Close() error
}

// Client wraps Redis operations for queue and cache
type Client struct {
	client *redis.Client
}

// Ensure Client implements ClientInterface
var _ ClientInterface = (*Client)(nil)

// Config contains Redis connection settings
type Config struct {
	Address  string
	Password string
	DB       int
}

// NewClient creates a new Redis client
func NewClient(config Config) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     config.Address,
		Password: config.Password,
		DB:       config.DB,
	})

	// Test connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}
	return &Client{
		client: rdb,
	}, nil
}

// Close closes the Redis connection
func (c *Client) Close() error {
	return c.client.Close()
}

// ServerState represents the provisioned server state to cache
// This is the format expected by LabMan with additional internal fields
type ServerState struct {
	User        string    `json:"user"`        // SSH username (e.g., "student")
	Address     string    `json:"address"`     // IPv6 address for SSH connection
	Status      string    `json:"status"`      // "provisioning" | "running" | "stopping" (normalized status)
	Available   bool      `json:"available"`   // true if server is ready for SSH connections (status == "running" for most providers)
	CloudStatus string    `json:"cloudStatus"` // Raw cloud provider status (e.g., "running", "starting", "initializing" from Hetzner)
	ServerID    string    `json:"serverId"`    // Internal: cloud provider server ID for deletion
	ExpiresAt   time.Time `json:"expiresAt"`   // Internal: timestamp for cleanup worker
	WebUserID   string    `json:"webUserId"`   // Internal: for cleanup to create decommission request
	LabID       int       `json:"labId"`       // Internal: for cleanup to create decommission request
}

// PopPayload pops a payload from the queue (blocking)
// Returns the raw string payload
func (c *Client) PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error) {
	result, err := c.client.BLPop(ctx, timeout, queueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return "", fmt.Errorf("no payload available in queue")
		}
		return "", fmt.Errorf("failed to pop from queue: %w", err)
	}

	if len(result) < 2 {
		return "", fmt.Errorf("unexpected response from Redis")
	}

	return result[1], nil
}

// PushPayload pushes a payload to the queue
func (c *Client) PushPayload(ctx context.Context, queueKey string, payload string) error {
	if err := c.client.RPush(ctx, queueKey, payload).Err(); err != nil {
		return fmt.Errorf("failed to push to queue: %w", err)
	}
	return nil
}

// ServerCacheKey constructs a cache key for a webuserid
// Note: labId is stored in the ServerState struct, not in the cache key
func ServerCacheKey(webuserid string) string {
	return fmt.Sprintf("vmmanager:servers:%s", webuserid)
}

// PushServerState pushes the provisioned server state to Redis cache
func (c *Client) PushServerState(ctx context.Context, cacheKey string, state ServerState, ttl time.Duration) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal server state: %w", err)
	}

	if err := c.client.Set(ctx, cacheKey, data, ttl).Err(); err != nil {
		return fmt.Errorf("failed to set cache: %w", err)
	}

	return nil
}

// GetServerState retrieves server state from cache
func (c *Client) GetServerState(ctx context.Context, cacheKey string) (*ServerState, error) {
	data, err := c.client.Get(ctx, cacheKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, fmt.Errorf("server state not found in cache")
		}
		return nil, fmt.Errorf("failed to get from cache: %w", err)
	}

	var state ServerState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal server state: %w", err)
	}

	return &state, nil
}

// GetAllServerStates returns all server states with the given prefix
func (c *Client) GetAllServerStates(ctx context.Context, prefix string) ([]ServerState, error) {
	var states []ServerState

	iter := c.client.Scan(ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		state, err := c.GetServerState(ctx, key)
		if err != nil {
			// Log scan error for visibility but continue processing other keys
			fmt.Printf("warning: failed to get server state for key %s: %v\n", key, err)
			continue
		}

		states = append(states, *state)
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	return states, nil
}

// DeleteServerState removes a server state from Redis cache
func (c *Client) DeleteServerState(ctx context.Context, cacheKey string) error {
	if err := c.client.Del(ctx, cacheKey).Err(); err != nil {
		return fmt.Errorf("failed to delete cache key: %w", err)
	}
	return nil
}

// RateLimitKey constructs a rate limit key for a user and operation
func RateLimitKey(webUserID string, operation string) string {
	return fmt.Sprintf("vmmanager:ratelimit:%s:%s", webUserID, operation)
}

// TryAcquireRateLimit attempts to acquire a rate limit lock atomically.
// Returns true if rate limit was acquired (proceed with operation).
// Returns false if rate limited (drop the message).
// Uses Redis SET NX for atomic operation.
func (c *Client) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	key := RateLimitKey(webUserID, operation)

	// Atomic SET NX with TTL - only succeeds if key doesn't exist
	success, err := c.client.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, fmt.Errorf("failed to acquire rate limit: %w", err)
	}

	return success, nil
}
