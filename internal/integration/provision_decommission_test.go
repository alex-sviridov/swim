package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/decommissioner"
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
	"log/slog"
	"os"
)

// TestInMemoryRedis implements redis.ClientInterface for testing
type TestInMemoryRedis struct {
	states     map[string]redis.ServerState
	queues     map[string][]string
	rateLimits map[string]bool // Track rate limit keys
}

func NewTestInMemoryRedis() *TestInMemoryRedis {
	return &TestInMemoryRedis{
		states:     make(map[string]redis.ServerState),
		queues:     make(map[string][]string),
		rateLimits: make(map[string]bool),
	}
}

func (c *TestInMemoryRedis) GetServerState(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
	state, ok := c.states[cacheKey]
	if !ok {
		return nil, fmt.Errorf("server state not found in cache")
	}
	return &state, nil
}

func (c *TestInMemoryRedis) PushServerState(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error {
	c.states[cacheKey] = state
	return nil
}

func (c *TestInMemoryRedis) DeleteServerState(ctx context.Context, cacheKey string) error {
	delete(c.states, cacheKey)
	return nil
}

func (c *TestInMemoryRedis) PushPayload(ctx context.Context, queueKey string, payload string) error {
	c.queues[queueKey] = append(c.queues[queueKey], payload)
	return nil
}

func (c *TestInMemoryRedis) PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error) {
	queue, ok := c.queues[queueKey]
	if !ok || len(queue) == 0 {
		return "", fmt.Errorf("no payload available")
	}

	payload := queue[0]
	c.queues[queueKey] = queue[1:]
	return payload, nil
}

func (c *TestInMemoryRedis) GetAllServerStates(ctx context.Context, prefix string) ([]redis.ServerState, error) {
	states := make([]redis.ServerState, 0)
	for key, state := range c.states {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			states = append(states, state)
		}
	}
	return states, nil
}

func (c *TestInMemoryRedis) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	// For integration tests, always allow (don't enforce rate limiting)
	// Integration tests are testing provisioning/decommissioning logic, not rate limiting
	return true, nil
}

func (c *TestInMemoryRedis) Close() error {
	return nil
}

func TestProvisioningAndDecommissioning_SameLabID(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewTestInMemoryRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(1 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	ctx := context.Background()

	// Test: Provision a server
	payload1 := `{"webuserid":"user-123","labId":42}`
	prov.ProcessRequest(ctx, payload1)

	// Wait for provisioning to complete
	time.Sleep(150 * time.Millisecond)

	// Verify server was created in cloud
	if mockConn.GetServerCount() != 1 {
		t.Fatalf("expected 1 server in cloud, got %d", mockConn.GetServerCount())
	}

	// Get server ID from cache
	cacheKey := redis.ServerCacheKey("user-123")
	state1, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server in cache, got error: %v", err)
	}
	firstServerID := state1.ServerID

	// Test: Send duplicate provision request with same labId
	payload2 := `{"webuserid":"user-123","labId":42}`
	prov.ProcessRequest(ctx, payload2)

	// Verify NO new server was created
	if mockConn.GetServerCount() != 1 {
		t.Errorf("expected 1 server in cloud (no duplicate), got %d", mockConn.GetServerCount())
	}

	// Verify cache still has same server
	state2, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server still in cache, got error: %v", err)
	}
	if state2.ServerID != firstServerID {
		t.Errorf("expected same server ID %s, got %s", firstServerID, state2.ServerID)
	}

	// Cleanup: Decommission the server
	decommPayload := fmt.Sprintf(`{"webuserid":"user-123","labId":42}`)
	decomm.ProcessRequest(ctx, decommPayload)

	// Verify server was deleted from cloud
	if mockConn.GetServerCount() != 0 {
		t.Errorf("expected 0 servers in cloud after decommission, got %d", mockConn.GetServerCount())
	}
}

func TestProvisioningAndDecommissioning_DifferentLabID(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewTestInMemoryRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(1 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	ctx := context.Background()

	// Test: Provision first server with labId=42
	payload1 := `{"webuserid":"user-123","labId":42}`
	prov.ProcessRequest(ctx, payload1)

	// Wait for provisioning
	time.Sleep(150 * time.Millisecond)

	// Verify first server was created
	if mockConn.GetServerCount() != 1 {
		t.Fatalf("expected 1 server in cloud, got %d", mockConn.GetServerCount())
	}

	cacheKey := redis.ServerCacheKey("user-123")
	state1, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server in cache, got error: %v", err)
	}
	firstServerID := state1.ServerID
	if state1.LabID != 42 {
		t.Errorf("expected LabID 42, got %d", state1.LabID)
	}

	// Test: Provision with different labId=99
	payload2 := `{"webuserid":"user-123","labId":99}`
	prov.ProcessRequest(ctx, payload2)

	// Give time for provisioning to complete
	time.Sleep(150 * time.Millisecond)

	// Verify decommission request was queued for old server
	decommQueue := redisClient.queues[config.DecommissionQueueKey]
	if len(decommQueue) != 1 {
		t.Fatalf("expected 1 decommission request queued, got %d", len(decommQueue))
	}

	// Verify new server was created
	if mockConn.GetServerCount() != 2 {
		t.Errorf("expected 2 servers in cloud (old + new), got %d", mockConn.GetServerCount())
	}

	// Verify cache has new server
	state2, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected new server in cache, got error: %v", err)
	}
	if state2.ServerID == firstServerID {
		t.Errorf("expected different server ID, got same: %s", state2.ServerID)
	}
	if state2.LabID != 99 {
		t.Errorf("expected new LabID 99, got %d", state2.LabID)
	}

	// Process the decommission request
	decommPayload, err := redisClient.PopPayload(ctx, config.DecommissionQueueKey, 1*time.Second)
	if err != nil {
		t.Fatalf("expected decommission payload, got error: %v", err)
	}
	t.Logf("Decommission payload: %s", decommPayload)
	decomm.ProcessRequest(ctx, decommPayload)

	// Give decommission time to complete
	time.Sleep(50 * time.Millisecond)

	// Verify old server was deleted from cloud
	_, err = mockConn.GetServerByID(firstServerID)
	if err == nil {
		t.Errorf("expected old server to be deleted from cloud, firstServerID=%s", firstServerID)
		// List all servers for debugging
		servers, _ := mockConn.ListServers()
		t.Logf("Remaining servers: %d", len(servers))
		for _, s := range servers {
			t.Logf("  - Server ID: %s", s.GetID())
		}
	}

	// Verify only new server remains in cloud
	actualCount := mockConn.GetServerCount()
	if actualCount != 1 {
		t.Errorf("expected 1 server in cloud (new server only), got %d", actualCount)
		// List all servers for debugging
		servers, _ := mockConn.ListServers()
		for _, s := range servers {
			t.Logf("  - Remaining server ID: %s", s.GetID())
		}
	}

	// Verify new server still exists
	_, err = mockConn.GetServerByID(state2.ServerID)
	if err != nil {
		t.Errorf("expected new server to still exist in cloud, got error: %v", err)
	}
}

func TestDecommissioning_CachelessDeletion(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewTestInMemoryRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	decomm := decommissioner.New(log, mockConn, redisClient)

	ctx := context.Background()

	// Manually create a server in cloud (simulating orphaned server)
	orphanedServer, _ := mockConn.CreateServer(`{"webuserid":"orphan","labId":1}`)
	orphanedServerID := orphanedServer.GetID()

	// Verify server exists in cloud
	if mockConn.GetServerCount() != 1 {
		t.Fatalf("expected 1 server in cloud, got %d", mockConn.GetServerCount())
	}

	// Send decommission request with serverID but no cache entry
	decommPayload := fmt.Sprintf(`{"webuserid":"user-orphan","serverId":"%s"}`, orphanedServerID)
	decomm.ProcessRequest(ctx, decommPayload)

	// Verify server was deleted from cloud even without cache entry
	if mockConn.GetServerCount() != 0 {
		t.Errorf("expected 0 servers in cloud after cache-less deletion, got %d", mockConn.GetServerCount())
	}

	_, err := mockConn.GetServerByID(orphanedServerID)
	if err == nil {
		t.Errorf("expected orphaned server to be deleted from cloud")
	}
}

func TestMultipleUsers_IndependentServers(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewTestInMemoryRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(1 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	ctx := context.Background()

	// Provision servers for multiple users
	payload1 := `{"webuserid":"user-1","labId":10}`
	payload2 := `{"webuserid":"user-2","labId":20}`
	payload3 := `{"webuserid":"user-3","labId":30}`

	prov.ProcessRequest(ctx, payload1)
	prov.ProcessRequest(ctx, payload2)
	prov.ProcessRequest(ctx, payload3)

	// Wait for all provisioning to complete
	time.Sleep(200 * time.Millisecond)

	// Verify 3 servers created
	if mockConn.GetServerCount() != 3 {
		t.Fatalf("expected 3 servers in cloud, got %d", mockConn.GetServerCount())
	}

	// Get server IDs
	state1, _ := redisClient.GetServerState(ctx, redis.ServerCacheKey("user-1"))
	state2, _ := redisClient.GetServerState(ctx, redis.ServerCacheKey("user-2"))
	state3, _ := redisClient.GetServerState(ctx, redis.ServerCacheKey("user-3"))

	// Decommission user-2's server only
	decommPayload := `{"webuserid":"user-2","labId":20}`
	decomm.ProcessRequest(ctx, decommPayload)

	// Verify only 2 servers remain
	if mockConn.GetServerCount() != 2 {
		t.Errorf("expected 2 servers in cloud after decommission, got %d", mockConn.GetServerCount())
	}

	// Verify user-1 and user-3 servers still exist
	_, err1 := mockConn.GetServerByID(state1.ServerID)
	_, err3 := mockConn.GetServerByID(state3.ServerID)
	if err1 != nil || err3 != nil {
		t.Errorf("expected user-1 and user-3 servers to still exist")
	}

	// Verify user-2 server was deleted
	_, err2 := mockConn.GetServerByID(state2.ServerID)
	if err2 == nil {
		t.Errorf("expected user-2 server to be deleted")
	}
}
