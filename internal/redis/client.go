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
	PushServerState(ctx context.Context, cacheKey string, state ServerState, ttl time.Duration) error
	GetServerState(ctx context.Context, cacheKey string) (*ServerState, error)
	GetExpiredServers(ctx context.Context, prefix string) ([]ServerState, error)
	DeleteServerState(ctx context.Context, cacheKey string) error
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
type ServerState struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	IPv6          string    `json:"ipv6"`
	State         string    `json:"state"`
	ProvisionedAt time.Time `json:"provisioned_at"`
	DeletionAt    time.Time `json:"deletion_at"`
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

// ServerCacheKey constructs a cache key for a server ID
func ServerCacheKey(serverID string) string {
	return fmt.Sprintf("swim:server:%s", serverID)
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

// GetExpiredServers returns server states where deletion_at is in the past
func (c *Client) GetExpiredServers(ctx context.Context, prefix string) ([]ServerState, error) {
	var expired []ServerState
	now := time.Now()

	iter := c.client.Scan(ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		state, err := c.GetServerState(ctx, key)
		if err != nil {
			// Log scan error for visibility but continue processing other keys
			fmt.Printf("warning: failed to get server state for key %s: %v\n", key, err)
			continue
		}

		if state.DeletionAt.Before(now) {
			expired = append(expired, *state)
		}
	}

	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan failed: %w", err)
	}

	return expired, nil
}

// DeleteServerState removes a server state from Redis cache
func (c *Client) DeleteServerState(ctx context.Context, cacheKey string) error {
	if err := c.client.Del(ctx, cacheKey).Err(); err != nil {
		return fmt.Errorf("failed to delete cache key: %w", err)
	}
	return nil
}
