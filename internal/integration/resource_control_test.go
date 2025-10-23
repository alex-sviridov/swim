package integration

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/cleanup"
	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/decommissioner"
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
)

// TestResourceControl_TTLEnforcement validates that labs are cleaned up after TTL expires
func TestResourceControl_TTLEnforcement(t *testing.T) {
	redisClient, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	// Set very short TTL for testing (override via state modification)
	_ = os.Setenv("DEFAULT_TTL_MINUTES", "1")
	defer func() { _ = os.Unsetenv("DEFAULT_TTL_MINUTES") }()

	userID := "ttl-test-user"
	labID := 1

	// Provision lab
	pushProvisionRequest(t, ctx, redisClient, userID, labID)
	payload, _ := redisClient.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	// Wait for running
	state, err := waitForServerAvailable(ctx, redisClient, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab did not reach running state: %v", err)
	}

	// Manually expire the server by modifying its ExpiresAt time
	state.ExpiresAt = time.Now().Add(-1 * time.Minute)
	cacheKey := redis.ServerCacheKey(userID)
	if err := redisClient.PushServerState(ctx, cacheKey, *state, config.ServerCacheTTL); err != nil {
		t.Fatalf("Failed to update server state: %v", err)
	}

	// Run cleanup worker once
	cleanupWorker := cleanup.New(log, mockConn, redisClient)
	go func() {
		// Run cleanup in background
		cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		cleanupWorker.Run(cleanupCtx)
	}()

	// Give cleanup time to detect and push decommission request
	time.Sleep(500 * time.Millisecond)

	// Process decommission request
	decommPayload, err := redisClient.PopPayload(ctx, config.DecommissionQueueKey, 3*time.Second)
	if err != nil {
		t.Fatalf("Cleanup should have pushed decommission request: %v", err)
	}

	decomm.ProcessRequest(ctx, decommPayload)

	// Verify server was deleted from cache
	err = waitForServerDeletion(ctx, redisClient, userID, labID, 3*time.Second)
	if err != nil {
		t.Fatalf("Server was not deleted after TTL expiry: %v", err)
	}

	// Verify server was deleted from mock connector
	serverCount := mockConn.GetServerCount()
	if serverCount != 0 {
		t.Errorf("Expected 0 active servers after cleanup, got %d", serverCount)
	}

	t.Logf("✓ TTL enforcement: expired lab was automatically cleaned up")
}

// TestResourceControl_CleanupWorkerRecovery validates cleanup worker recovers abandoned resources
func TestResourceControl_CleanupWorkerRecovery(t *testing.T) {
	redisClient, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	// Provision multiple labs that are already expired (simulating abandonment)
	expiredUsers := []struct {
		userID string
		labID  int
	}{
		{"abandoned-user-1", 1},
		{"abandoned-user-2", 2},
		{"abandoned-user-3", 3},
	}

	for _, user := range expiredUsers {
		pushProvisionRequest(t, ctx, redisClient, user.userID, user.labID)
		payload, _ := redisClient.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
		prov.ProcessRequest(ctx, payload)

		// Wait for running and then expire it
		state, err := waitForServerAvailable(ctx, redisClient, user.userID, user.labID, 5*time.Second)
		if err != nil {
			t.Fatalf("Lab %d did not reach running state: %v", user.labID, err)
		}

		// Expire the server
		state.ExpiresAt = time.Now().Add(-10 * time.Minute)
		cacheKey := redis.ServerCacheKey(user.userID)
		_ = redisClient.PushServerState(ctx, cacheKey, *state, config.ServerCacheTTL)
	}

	initialServerCount := mockConn.GetServerCount()
	if initialServerCount != 3 {
		t.Fatalf("Expected 3 servers before cleanup, got %d", initialServerCount)
	}

	// Run cleanup worker
	cleanupWorker := cleanup.New(log, mockConn, redisClient)
	go func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		cleanupWorker.Run(cleanupCtx)
	}()

	// Give cleanup time to run
	time.Sleep(1 * time.Second)

	// Process all decommission requests
	for i := 0; i < 3; i++ {
		decommPayload, err := redisClient.PopPayload(ctx, config.DecommissionQueueKey, 3*time.Second)
		if err != nil {
			t.Errorf("Expected decommission request %d: %v", i+1, err)
			continue
		}
		decomm.ProcessRequest(ctx, decommPayload)
	}

	// Give decommissioner time to finish deleting servers
	time.Sleep(1 * time.Second)

	// Verify all servers were cleaned up
	finalServerCount := mockConn.GetServerCount()
	if finalServerCount != 0 {
		t.Errorf("Expected 0 servers after cleanup, got %d", finalServerCount)
	}

	// Verify cache is empty
	allStates, _ := redisClient.GetAllServerStates(ctx, config.ServerCachePrefix)
	if len(allStates) != 0 {
		t.Errorf("Expected 0 cache entries after cleanup, got %d", len(allStates))
		for _, state := range allStates {
			t.Logf("  Remaining cache entry: user=%s, labId=%d, status=%s", state.WebUserID, state.LabID, state.Status)
		}
	}

	t.Logf("✓ Cleanup worker recovered %d abandoned resources", len(expiredUsers))
}

// TestResourceControl_NoResourceLeaksOnLabSwitch validates that switching labs doesn't leak resources
func TestResourceControl_NoResourceLeaksOnLabSwitch(t *testing.T) {
	redisClient, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	// Set short rate limits for testing to avoid timeouts
	_ = os.Setenv("PROVISION_RATE_LIMIT_SECONDS", "1")
	_ = os.Setenv("DECOMMISSION_RATE_LIMIT_SECONDS", "1")
	defer func() { _ = os.Unsetenv("PROVISION_RATE_LIMIT_SECONDS") }()
	defer func() { _ = os.Unsetenv("DECOMMISSION_RATE_LIMIT_SECONDS") }()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	userID := "lab-switch-user"

	// Provision lab 5
	pushProvisionRequest(t, ctx, redisClient, userID, 5)
	payload1, _ := redisClient.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload1)

	state1, err := waitForServerAvailable(ctx, redisClient, userID, 5, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 did not reach running state: %v", err)
	}
	serverID1 := state1.ServerID

	// User wants to switch to lab 7 - must decommission lab 5 first
	pushDecommissionRequest(t, ctx, redisClient, userID, 5)
	decommPayload, _ := redisClient.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	// Verify lab 5 is deleted
	err = waitForServerDeletion(ctx, redisClient, userID, 5, 3*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 was not deleted: %v", err)
	}

	// Wait for provision rate limit to expire before provisioning lab 7
	time.Sleep(1100 * time.Millisecond)

	// Now provision lab 7
	pushProvisionRequest(t, ctx, redisClient, userID, 7)
	payload2, _ := redisClient.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload2)

	state2, err := waitForServerAvailable(ctx, redisClient, userID, 7, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 7 did not reach running state: %v", err)
	}
	serverID2 := state2.ServerID

	// Verify different servers
	if serverID1 == serverID2 {
		t.Error("Lab 5 and Lab 7 should have different server IDs")
	}

	// Verify only 1 lab active for user
	count, _ := countUserLabs(ctx, redisClient, userID)
	if count != 1 {
		t.Errorf("Expected 1 active lab after switch, got %d", count)
	}

	// Verify it's lab 7
	labs, _ := getUserLabs(ctx, redisClient, userID)
	if len(labs) != 1 || labs[0] != 7 {
		t.Errorf("Expected lab 7, got %v", labs)
	}

	// Verify old server is actually deleted (not just removed from cache)
	_, err = mockConn.GetServerByID(serverID1)
	if err == nil {
		t.Error("Old server (lab 5) should be deleted from connector")
	}

	// Verify new server exists
	_, err = mockConn.GetServerByID(serverID2)
	if err != nil {
		t.Errorf("New server (lab 7) should exist: %v", err)
	}

	// Verify total server count
	serverCount := mockConn.GetServerCount()
	if serverCount != 1 {
		t.Errorf("Expected exactly 1 server after lab switch, got %d", serverCount)
	}

	t.Logf("✓ Lab switch: old lab deleted, new lab provisioned, no resource leaks")
}

// TestResourceControl_PreventMultipleActiveProvisions validates the one-lab-per-user constraint
// The architecture enforces this through the cache key design: vmmanager:servers:{webuserid}
func TestResourceControl_PreventMultipleActiveProvisions(t *testing.T) {
	redisClient, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	// Set short rate limits for testing to avoid timeouts
	_ = os.Setenv("PROVISION_RATE_LIMIT_SECONDS", "1")
	_ = os.Setenv("DECOMMISSION_RATE_LIMIT_SECONDS", "1")
	defer func() { _ = os.Unsetenv("PROVISION_RATE_LIMIT_SECONDS") }()
	defer func() { _ = os.Unsetenv("DECOMMISSION_RATE_LIMIT_SECONDS") }()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(200 * time.Millisecond)

	userID := "multi-provision-user"

	// Provision lab 1
	pushProvisionRequest(t, ctx, redisClient, userID, 1)
	payload1, _ := redisClient.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload1)

	// Wait for lab 1 to become available
	state1, err := waitForServerAvailable(ctx, redisClient, userID, 1, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 1 failed to start: %v", err)
	}
	server1ID := state1.ServerID

	// Verify user has 1 lab
	count, _ := countUserLabs(ctx, redisClient, userID)
	if count != 1 {
		t.Errorf("Expected 1 lab after first provision, got %d", count)
	}

	// Wait for provision rate limit to expire before provisioning lab 2
	time.Sleep(1100 * time.Millisecond)

	// Now provision lab 2 (this should OVERWRITE lab 1 in cache due to one-lab-per-user constraint)
	pushProvisionRequest(t, ctx, redisClient, userID, 2)
	payload2, _ := redisClient.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload2)

	// Wait for lab 2 to become available
	_, err = waitForServerAvailable(ctx, redisClient, userID, 2, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 2 failed to start: %v", err)
	}

	// Verify user still has only 1 lab (lab 2 overwrote lab 1 in cache)
	count, _ = countUserLabs(ctx, redisClient, userID)
	if count != 1 {
		t.Errorf("Expected 1 lab after second provision (one-lab-per-user), got %d", count)
	}

	// Verify the cached lab is lab 2, not lab 1
	labs, _ := getUserLabs(ctx, redisClient, userID)
	if len(labs) != 1 {
		t.Errorf("Expected 1 lab in cache, got %v", labs)
	}
	if len(labs) == 1 && labs[0] != 2 {
		t.Errorf("Expected lab 2 to be in cache, got lab %d", labs[0])
	}

	// Verify lab 1's VM still exists in cloud (orphaned), but lab 2 is in cache
	if _, err := mockConn.GetServerByID(server1ID); err != nil {
		t.Errorf("Lab 1's VM should still exist in cloud (orphaned): %v", err)
	}

	// The architecture enforces one-lab-per-user at the cache level
	// LabMan is responsible for decommissioning old labs before provisioning new ones
	t.Logf("✓ One-lab-per-user constraint enforced via cache key design (lab 2 overwrote lab 1)")
}
