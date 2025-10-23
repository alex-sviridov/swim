package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/redis"
)

// setupTestRedis creates a Redis client for testing
func setupTestRedis(t *testing.T) (*redis.Client, func()) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Use environment variable or default to localhost
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	// Use a unique DB for each test to avoid collisions
	db := 10 + (int(time.Now().UnixNano()) % 5)

	client, err := redis.NewClient(redis.Config{
		Address:  addr,
		Password: "",
		DB:       db,
	})
	if err != nil {
		t.Fatalf("Failed to connect to Redis: %v", err)
	}

	// Cleanup function
	cleanup := func() {
		// Clean all test keys
		client.Close()
	}

	// Flush the test DB to ensure clean state
	ctx := context.Background()
	if err := flushTestDB(ctx, client); err != nil {
		t.Fatalf("Failed to flush test DB: %v", err)
	}

	return client, cleanup
}

// flushTestDB flushes all keys in the test database
func flushTestDB(ctx context.Context, client *redis.Client) error {
	// Delete all vmmanager keys
	patterns := []string{
		config.ProvisionQueueKey,
		config.DecommissionQueueKey,
		config.ServerCachePrefix + "*",
	}

	for _, pattern := range patterns {
		if err := deleteKeysByPattern(ctx, client, pattern); err != nil {
			return err
		}
	}
	return nil
}

// deleteKeysByPattern deletes all keys matching a pattern
func deleteKeysByPattern(ctx context.Context, client *redis.Client, pattern string) error {
	// For queue keys (exact match)
	if pattern == config.ProvisionQueueKey || pattern == config.DecommissionQueueKey {
		return client.DeleteServerState(ctx, pattern)
	}

	// For pattern matching (cache keys)
	states, err := client.GetAllServerStates(ctx, config.ServerCachePrefix)
	if err != nil {
		return nil // No keys to delete
	}

	for _, state := range states {
		cacheKey := redis.ServerCacheKey(state.WebUserID)
		client.DeleteServerState(ctx, cacheKey)
	}

	return nil
}

// pushProvisionRequest pushes a provision request to the queue
func pushProvisionRequest(t *testing.T, ctx context.Context, client *redis.Client, webUserID string, labID int) {
	req := map[string]interface{}{
		"webuserid": webUserID,
		"labId":     labID,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal provision request: %v", err)
	}

	if err := client.PushPayload(ctx, config.ProvisionQueueKey, string(payload)); err != nil {
		t.Fatalf("Failed to push provision request: %v", err)
	}
}

// pushDecommissionRequest pushes a decommission request to the queue
func pushDecommissionRequest(t *testing.T, ctx context.Context, client *redis.Client, webUserID string, labID int) {
	req := map[string]interface{}{
		"webuserid": webUserID,
		"labId":     labID,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal decommission request: %v", err)
	}

	if err := client.PushPayload(ctx, config.DecommissionQueueKey, string(payload)); err != nil {
		t.Fatalf("Failed to push decommission request: %v", err)
	}
}

// waitForServerState polls for a server to reach expected status
func waitForServerState(ctx context.Context, client *redis.Client, webUserID string, labID int, expectedStatus string, timeout time.Duration) (*redis.ServerState, error) {
	cacheKey := redis.ServerCacheKey(webUserID)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		state, err := client.GetServerState(ctx, cacheKey)
		if err == nil && state.Status == expectedStatus && state.LabID == labID {
			return state, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return nil, fmt.Errorf("timeout waiting for server status %s", expectedStatus)
}

// waitForServerAvailable polls for a server to become available (available == true)
func waitForServerAvailable(ctx context.Context, client *redis.Client, webUserID string, labID int, timeout time.Duration) (*redis.ServerState, error) {
	cacheKey := redis.ServerCacheKey(webUserID)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		state, err := client.GetServerState(ctx, cacheKey)
		if err == nil && state.Available && state.LabID == labID {
			return state, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return nil, fmt.Errorf("timeout waiting for server to become available")
}

// waitForServerDeletion polls for a server to be deleted from cache
func waitForServerDeletion(ctx context.Context, client *redis.Client, webUserID string, labID int, timeout time.Duration) error {
	cacheKey := redis.ServerCacheKey(webUserID)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		state, err := client.GetServerState(ctx, cacheKey)
		if err != nil {
			// Server not found - deletion successful
			return nil
		}
		// Also check if it's a different lab now (switched to different lab)
		if state.LabID != labID {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	return fmt.Errorf("timeout waiting for server deletion")
}

// countUserLabs returns the number of active labs for a user
func countUserLabs(ctx context.Context, client *redis.Client, webUserID string) (int, error) {
	allStates, err := client.GetAllServerStates(ctx, config.ServerCachePrefix)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, state := range allStates {
		if state.WebUserID == webUserID && state.Status != config.StatusStopping {
			count++
		}
	}

	return count, nil
}

// getUserLabs returns all active lab IDs for a user
func getUserLabs(ctx context.Context, client *redis.Client, webUserID string) ([]int, error) {
	allStates, err := client.GetAllServerStates(ctx, config.ServerCachePrefix)
	if err != nil {
		return nil, err
	}

	var labIDs []int
	for _, state := range allStates {
		if state.WebUserID == webUserID && state.Status != config.StatusStopping {
			labIDs = append(labIDs, state.LabID)
		}
	}

	return labIDs, nil
}

// assertServerState validates server state fields
func assertServerState(t *testing.T, state *redis.ServerState, expectedStatus string, expectedUser string) {
	if state.Status != expectedStatus {
		t.Errorf("Expected status %s, got %s", expectedStatus, state.Status)
	}

	if state.User != expectedUser {
		t.Errorf("Expected user %s, got %s", expectedUser, state.User)
	}

	if state.Address == "" && expectedStatus == config.StatusRunning {
		t.Error("Expected non-empty address for running server")
	}

	if state.ServerID == "" {
		t.Error("Expected non-empty server ID")
	}
}

// assertServerAvailable validates that server is available and ready for SSH
func assertServerAvailable(t *testing.T, state *redis.ServerState) {
	if !state.Available {
		t.Errorf("Expected server to be available, but available=%v", state.Available)
	}

	if state.Status != config.StatusRunning {
		t.Errorf("Available server should have status=running, got %s", state.Status)
	}

	if state.Address == "" {
		t.Error("Available server must have non-empty address")
	}

	if state.CloudStatus == "" {
		t.Error("CloudStatus should not be empty")
	}

	if state.ServerID == "" {
		t.Error("ServerID should not be empty")
	}
}
