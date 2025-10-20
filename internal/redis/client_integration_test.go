package redis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// Integration tests require a running Redis instance
// These tests can be skipped with: go test -short

func setupTestRedis(t *testing.T) (*Client, func()) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Use environment variable or default to localhost
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client, err := NewClient(Config{
		Address:  addr,
		Password: "",
		DB:       15, // Use a separate test database
	})
	if err != nil {
		t.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Cleanup function
	cleanup := func() {
		ctx := context.Background()
		// Clean test keys
		client.client.FlushDB(ctx)
		client.Close()
	}

	return client, cleanup
}

func TestNewClient(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tests := []struct {
		name      string
		config    Config
		wantError bool
	}{
		{
			name: "valid connection",
			config: Config{
				Address:  "localhost:6379",
				Password: "",
				DB:       15,
			},
			wantError: false,
		},
		{
			name: "invalid address",
			config: Config{
				Address:  "invalid:9999",
				Password: "",
				DB:       0,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if client != nil {
					client.Close()
				}
			}
		})
	}
}

func TestServerCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		serverID string
		want     string
	}{
		{
			name:     "simple id",
			serverID: "server-123",
			want:     "swim:server:server-123",
		},
		{
			name:     "uuid",
			serverID: "550e8400-e29b-41d4-a716-446655440000",
			want:     "swim:server:550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:     "empty id",
			serverID: "",
			want:     "swim:server:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServerCacheKey(tt.serverID)
			if got != tt.want {
				t.Errorf("ServerCacheKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPushAndGetServerState(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second) // Truncate for comparison

	state := ServerState{
		ID:            "test-server-1",
		Name:          "test-server",
		IPv6:          "2001:db8::1",
		State:         "booted",
		ProvisionedAt: now,
		DeletionAt:    now.Add(1 * time.Hour),
	}

	cacheKey := ServerCacheKey(state.ID)

	// Push state
	err := client.PushServerState(ctx, cacheKey, state, 10*time.Minute)
	if err != nil {
		t.Fatalf("PushServerState failed: %v", err)
	}

	// Get state
	retrieved, err := client.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("GetServerState failed: %v", err)
	}

	// Verify state
	if retrieved.ID != state.ID {
		t.Errorf("ID = %v, want %v", retrieved.ID, state.ID)
	}
	if retrieved.Name != state.Name {
		t.Errorf("Name = %v, want %v", retrieved.Name, state.Name)
	}
	if retrieved.IPv6 != state.IPv6 {
		t.Errorf("IPv6 = %v, want %v", retrieved.IPv6, state.IPv6)
	}
	if retrieved.State != state.State {
		t.Errorf("State = %v, want %v", retrieved.State, state.State)
	}
	if !retrieved.ProvisionedAt.Equal(state.ProvisionedAt) {
		t.Errorf("ProvisionedAt = %v, want %v", retrieved.ProvisionedAt, state.ProvisionedAt)
	}
	if !retrieved.DeletionAt.Equal(state.DeletionAt) {
		t.Errorf("DeletionAt = %v, want %v", retrieved.DeletionAt, state.DeletionAt)
	}
}

func TestGetServerStateNotFound(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	cacheKey := ServerCacheKey("nonexistent-server")

	_, err := client.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Error("Expected error for non-existent key, got nil")
	}
}

func TestPopPayload(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	queueKey := "test:queue"
	testPayload := `{"ServerType":"DEV1-S","WebUsername":"testuser","WebLabID":1,"TTLMinutes":60}`

	// Push payload to queue
	err := client.client.RPush(ctx, queueKey, testPayload).Err()
	if err != nil {
		t.Fatalf("Failed to push to queue: %v", err)
	}

	// Pop payload
	payload, err := client.PopPayload(ctx, queueKey, 5*time.Second)
	if err != nil {
		t.Fatalf("PopPayload failed: %v", err)
	}

	if payload != testPayload {
		t.Errorf("PopPayload = %v, want %v", payload, testPayload)
	}
}

func TestPopPayloadTimeout(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	queueKey := "test:empty:queue"

	// Try to pop from empty queue with short timeout
	_, err := client.PopPayload(ctx, queueKey, 1*time.Second)
	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

func TestGetExpiredServers(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Create test servers
	states := []ServerState{
		{
			ID:            "expired-1",
			Name:          "Expired Server 1",
			IPv6:          "2001:db8::1",
			State:         "booted",
			ProvisionedAt: now.Add(-2 * time.Hour),
			DeletionAt:    now.Add(-1 * time.Hour), // Expired
		},
		{
			ID:            "expired-2",
			Name:          "Expired Server 2",
			IPv6:          "2001:db8::2",
			State:         "booted",
			ProvisionedAt: now.Add(-3 * time.Hour),
			DeletionAt:    now.Add(-30 * time.Minute), // Expired
		},
		{
			ID:            "active-1",
			Name:          "Active Server",
			IPv6:          "2001:db8::3",
			State:         "booted",
			ProvisionedAt: now,
			DeletionAt:    now.Add(1 * time.Hour), // Not expired
		},
	}

	// Push all states
	for _, state := range states {
		cacheKey := ServerCacheKey(state.ID)
		err := client.PushServerState(ctx, cacheKey, state, 10*time.Minute)
		if err != nil {
			t.Fatalf("Failed to push state: %v", err)
		}
	}

	// Get expired servers
	expired, err := client.GetExpiredServers(ctx, "swim:server:")
	if err != nil {
		t.Fatalf("GetExpiredServers failed: %v", err)
	}

	// Verify we got exactly 2 expired servers
	if len(expired) != 2 {
		t.Errorf("Expected 2 expired servers, got %d", len(expired))
	}

	// Verify expired server IDs
	expiredIDs := make(map[string]bool)
	for _, state := range expired {
		expiredIDs[state.ID] = true
	}

	if !expiredIDs["expired-1"] {
		t.Error("Expected expired-1 in results")
	}
	if !expiredIDs["expired-2"] {
		t.Error("Expected expired-2 in results")
	}
	if expiredIDs["active-1"] {
		t.Error("Active server should not be in expired results")
	}
}

func TestGetExpiredServersEmpty(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()

	// Get expired servers from empty database
	expired, err := client.GetExpiredServers(ctx, "swim:server:")
	if err != nil {
		t.Fatalf("GetExpiredServers failed: %v", err)
	}

	if len(expired) != 0 {
		t.Errorf("Expected 0 expired servers, got %d", len(expired))
	}
}

func TestConcurrentOperations(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Test concurrent writes
	done := make(chan bool)
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			state := ServerState{
				ID:            fmt.Sprintf("concurrent-%d", id),
				Name:          fmt.Sprintf("Server %d", id),
				IPv6:          fmt.Sprintf("2001:db8::%d", id),
				State:         "booted",
				ProvisionedAt: now,
				DeletionAt:    now.Add(1 * time.Hour),
			}

			cacheKey := ServerCacheKey(state.ID)
			err := client.PushServerState(ctx, cacheKey, state, 5*time.Minute)
			if err != nil {
				errors <- err
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check for errors
	select {
	case err := <-errors:
		t.Errorf("Concurrent operation failed: %v", err)
	default:
		// No errors
	}

	// Verify all states were written
	expired, err := client.GetExpiredServers(ctx, "swim:server:")
	if err != nil {
		t.Fatalf("GetExpiredServers failed: %v", err)
	}

	// We should find 0 expired (all have future deletion times)
	if len(expired) != 0 {
		t.Errorf("Expected 0 expired servers, got %d", len(expired))
	}
}
