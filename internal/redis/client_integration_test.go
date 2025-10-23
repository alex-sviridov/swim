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
		_ = client.Close()
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
					_ = client.Close()
				}
			}
		})
	}
}

func TestServerCacheKey(t *testing.T) {
	tests := []struct {
		name      string
		webUserID string
		want      string
	}{
		{
			name:      "simple user id",
			webUserID: "user-123",
			want:      "vmmanager:servers:user-123",
		},
		{
			name:      "uuid",
			webUserID: "550e8400-e29b-41d4-a716-446655440000",
			want:      "vmmanager:servers:550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name:      "empty id",
			webUserID: "",
			want:      "vmmanager:servers:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServerCacheKey(tt.webUserID)
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
		User:        "student",
		Address:     "2001:db8::1",
		Status:      "running",
		Available:   true,
		CloudStatus: "running",
		ServerID:    "test-server-1",
		ExpiresAt:   now.Add(1 * time.Hour),
		WebUserID:   "user-123",
		LabID:       5,
	}

	cacheKey := ServerCacheKey(state.WebUserID)

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
	if retrieved.User != state.User {
		t.Errorf("User = %v, want %v", retrieved.User, state.User)
	}
	if retrieved.Address != state.Address {
		t.Errorf("Address = %v, want %v", retrieved.Address, state.Address)
	}
	if retrieved.Status != state.Status {
		t.Errorf("Status = %v, want %v", retrieved.Status, state.Status)
	}
	if retrieved.Available != state.Available {
		t.Errorf("Available = %v, want %v", retrieved.Available, state.Available)
	}
	if retrieved.CloudStatus != state.CloudStatus {
		t.Errorf("CloudStatus = %v, want %v", retrieved.CloudStatus, state.CloudStatus)
	}
	if retrieved.ServerID != state.ServerID {
		t.Errorf("ServerID = %v, want %v", retrieved.ServerID, state.ServerID)
	}
	if retrieved.WebUserID != state.WebUserID {
		t.Errorf("WebUserID = %v, want %v", retrieved.WebUserID, state.WebUserID)
	}
	if retrieved.LabID != state.LabID {
		t.Errorf("LabID = %v, want %v", retrieved.LabID, state.LabID)
	}
	if !retrieved.ExpiresAt.Truncate(time.Second).Equal(state.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", retrieved.ExpiresAt, state.ExpiresAt)
	}
}

func TestGetServerStateNotFound(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	cacheKey := ServerCacheKey("nonexistent-user")

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
	testPayload := `{"ServerType":"DEV1-S","WebUsername":"testuser","LabID":1,"TTLMinutes":60}`

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

func TestGetAllServerStates(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Create test servers for different users
	states := []ServerState{
		{
			User:        "student",
			Address:     "2001:db8::1",
			Status:      "running",
			Available:   true,
			CloudStatus: "running",
			ServerID:    "server-1",
			ExpiresAt:   now.Add(1 * time.Hour),
			WebUserID:   "user-1",
			LabID:       5,
		},
		{
			User:        "student",
			Address:     "2001:db8::2",
			Status:      "running",
			Available:   true,
			CloudStatus: "running",
			ServerID:    "server-2",
			ExpiresAt:   now.Add(2 * time.Hour),
			WebUserID:   "user-2",
			LabID:       3,
		},
		{
			User:        "student",
			Address:     "2001:db8::3",
			Status:      "provisioning",
			Available:   false,
			CloudStatus: "starting",
			ServerID:    "server-3",
			ExpiresAt:   now.Add(1 * time.Hour),
			WebUserID:   "user-3",
			LabID:       7,
		},
	}

	// Push all states
	for _, state := range states {
		cacheKey := ServerCacheKey(state.WebUserID)
		err := client.PushServerState(ctx, cacheKey, state, 10*time.Minute)
		if err != nil {
			t.Fatalf("Failed to push state: %v", err)
		}
	}

	// Get all server states
	allStates, err := client.GetAllServerStates(ctx, "vmmanager:servers:")
	if err != nil {
		t.Fatalf("GetAllServerStates failed: %v", err)
	}

	// Verify we got all 3 servers
	if len(allStates) != 3 {
		t.Errorf("Expected 3 servers, got %d", len(allStates))
	}

	// Verify server data
	userIDs := make(map[string]bool)
	for _, state := range allStates {
		userIDs[state.WebUserID] = true
	}

	if !userIDs["user-1"] || !userIDs["user-2"] || !userIDs["user-3"] {
		t.Error("Missing expected users in results")
	}
}

func TestConcurrentOperations(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().UTC()

	// Test concurrent writes for different users
	done := make(chan bool)
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		go func(id int) {
			state := ServerState{
				User:        "student",
				Address:     fmt.Sprintf("2001:db8::%d", id),
				Status:      "running",
				Available:   true,
				CloudStatus: "running",
				ServerID:    fmt.Sprintf("server-%d", id),
				ExpiresAt:   now.Add(1 * time.Hour),
				WebUserID:   fmt.Sprintf("concurrent-user-%d", id),
				LabID:       id + 1,
			}

			cacheKey := ServerCacheKey(state.WebUserID)
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
	allStates, err := client.GetAllServerStates(ctx, "vmmanager:servers:")
	if err != nil {
		t.Fatalf("GetAllServerStates failed: %v", err)
	}

	// We should have 10 states
	if len(allStates) != 10 {
		t.Errorf("Expected 10 servers, got %d", len(allStates))
	}
}
