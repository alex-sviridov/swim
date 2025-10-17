package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps Redis operations for queue and cache
type Client struct {
	client *redis.Client
	ctx    context.Context
}

// Config contains Redis connection settings
type Config struct {
	Address  string
	Password string
	DB       int
}

// NewClient creates a new Redis client
// connectionString can be either a Redis URL (redis://host:port) or just host:port
func NewClient(config Config) (*Client, error) {
	var rdb *redis.Client

	// Try to parse as Redis URL first
	opt, err := redis.ParseURL(config.Address)
	if err != nil {
		// If parsing fails, assume it's a simple address format (host:port)
		rdb = redis.NewClient(&redis.Options{
			Addr:     config.Address,
			Password: config.Password,
			DB:       config.DB,
		})
	} else {
		// Use parsed options, but override with config if provided
		if config.Password != "" {
			opt.Password = config.Password
		}
		if config.DB != 0 {
			opt.DB = config.DB
		}
		rdb = redis.NewClient(opt)
	}

	ctx := context.Background()

	// Test connection
	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}
	return &Client{
		client: rdb,
		ctx:    ctx,
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
func (c *Client) PopPayload(queueKey string, timeout time.Duration) (string, error) {
	result, err := c.client.BLPop(c.ctx, timeout, queueKey).Result()
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

// PushServerState pushes the provisioned server state to Redis cache
func (c *Client) PushServerState(cacheKey string, state ServerState, ttl time.Duration) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal server state: %w", err)
	}

	if err := c.client.Set(c.ctx, cacheKey, data, ttl).Err(); err != nil {
		return fmt.Errorf("failed to set cache: %w", err)
	}

	return nil
}

// GetServerState retrieves server state from cache
func (c *Client) GetServerState(cacheKey string) (*ServerState, error) {
	data, err := c.client.Get(c.ctx, cacheKey).Result()
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
