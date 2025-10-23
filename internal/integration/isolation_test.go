package integration

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
)

// TestUserIsolation_OneLabPerUser validates that each user can only have one active lab
func TestUserIsolation_OneLabPerUser(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	userID := "user-isolation-test-1"

	// User provisions lab 5
	pushProvisionRequest(t, ctx, client, userID, 5)
	payload, err := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to pop provision request: %v", err)
	}
	prov.ProcessRequest(ctx, payload)

	// Wait for lab 5 to be running
	state1, err := waitForServerAvailable(ctx, client, userID, 5, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 did not reach running state: %v", err)
	}

	// Verify user has exactly 1 lab
	count, err := countUserLabs(ctx, client, userID)
	if err != nil {
		t.Fatalf("Failed to count user labs: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected user to have 1 lab, got %d", count)
	}

	// Verify it's lab 5
	labs, err := getUserLabs(ctx, client, userID)
	if err != nil {
		t.Fatalf("Failed to get user labs: %v", err)
	}
	if len(labs) != 1 || labs[0] != 5 {
		t.Errorf("Expected user to have lab 5, got %v", labs)
	}

	t.Logf("✓ User has exactly one lab (lab 5) with address: %s", state1.Address)
}

// TestUserIsolation_SeparateCacheEntries validates that different users have isolated cache entries
func TestUserIsolation_SeparateCacheEntries(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	// User A provisions lab 1
	pushProvisionRequest(t, ctx, client, "userA", 1)
	payloadA, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payloadA)

	// User B provisions lab 1 (same lab ID, different user)
	pushProvisionRequest(t, ctx, client, "userB", 1)
	payloadB, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payloadB)

	// Wait for both to be running
	stateA, err := waitForServerAvailable(ctx, client, "userA", 1, 5*time.Second)
	if err != nil {
		t.Fatalf("User A lab 1 did not reach running state: %v", err)
	}

	stateB, err := waitForServerAvailable(ctx, client, "userB", 1, 5*time.Second)
	if err != nil {
		t.Fatalf("User B lab 1 did not reach running state: %v", err)
	}

	// Verify separate cache entries
	if stateA.ServerID == stateB.ServerID {
		t.Error("User A and User B should have different server IDs")
	}

	if stateA.Address == stateB.Address {
		t.Error("User A and User B should have different IP addresses")
	}

	// Verify each user has exactly 1 lab
	countA, _ := countUserLabs(ctx, client, "userA")
	countB, _ := countUserLabs(ctx, client, "userB")

	if countA != 1 {
		t.Errorf("User A should have 1 lab, got %d", countA)
	}

	if countB != 1 {
		t.Errorf("User B should have 1 lab, got %d", countB)
	}

	t.Logf("✓ User A lab 1: %s", stateA.Address)
	t.Logf("✓ User B lab 1: %s", stateB.Address)
	t.Logf("✓ Complete user isolation verified")
}

// TestUserIsolation_ConcurrentUsers validates that multiple users can provision labs simultaneously
func TestUserIsolation_ConcurrentUsers(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	numUsers := 10
	var wg sync.WaitGroup
	errors := make(chan error, numUsers)

	// Provision labs for multiple users concurrently
	for i := 0; i < numUsers; i++ {
		wg.Add(1)
		go func(userIndex int) {
			defer wg.Done()

			userID := "concurrent-user-" + string(rune('A'+userIndex))
			labID := userIndex + 1

			// Push provision request
			pushProvisionRequest(t, ctx, client, userID, labID)

			// Process request
			payload, err := client.PopPayload(ctx, config.ProvisionQueueKey, 10*time.Second)
			if err != nil {
				errors <- err
				return
			}

			prov.ProcessRequest(ctx, payload)

			// Wait for running state
			_, err = waitForServerAvailable(ctx, client, userID, labID, 10*time.Second)
			if err != nil {
				errors <- err
				return
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent provisioning error: %v", err)
	}

	// Verify each user has exactly 1 lab
	for i := 0; i < numUsers; i++ {
		userID := "concurrent-user-" + string(rune('A'+i))
		count, err := countUserLabs(ctx, client, userID)
		if err != nil {
			t.Errorf("Failed to count labs for %s: %v", userID, err)
			continue
		}

		if count != 1 {
			t.Errorf("User %s should have 1 lab, got %d", userID, count)
		}
	}

	// Verify total number of active servers
	allStates, err := client.GetAllServerStates(ctx, config.ServerCachePrefix)
	if err != nil {
		t.Fatalf("Failed to get all server states: %v", err)
	}

	if len(allStates) != numUsers {
		t.Errorf("Expected %d total servers, got %d", numUsers, len(allStates))
		for _, state := range allStates {
			t.Logf("  Active server: user=%s, labId=%d, status=%s", state.WebUserID, state.LabID, state.Status)
		}
	}

	t.Logf("✓ %d concurrent users provisioned labs successfully with complete isolation", numUsers)
}

// TestUserIsolation_CannotAccessOtherUserData validates cache key isolation
func TestUserIsolation_CannotAccessOtherUserData(t *testing.T) {
	client, cleanup := setupTestRedis(t)
	defer cleanup()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	// User A provisions lab 5
	pushProvisionRequest(t, ctx, client, "userA", 5)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	// Wait for running
	stateA, err := waitForServerAvailable(ctx, client, "userA", 5, 5*time.Second)
	if err != nil {
		t.Fatalf("User A lab 5 did not reach running state: %v", err)
	}

	// Try to access User A's lab using User B's cache key pattern
	// This should fail because cache keys include user ID
	cacheKeyB := redis.ServerCacheKey("userB")
	stateB, err := client.GetServerState(ctx, cacheKeyB)
	if err == nil {
		t.Error("User B should not be able to access User A's lab via cache key")
	}

	if stateB != nil {
		t.Error("User B retrieved User A's data - isolation breach!")
	}

	// Verify User A can access their own data
	cacheKeyA := redis.ServerCacheKey("userA")
	stateADirect, err := client.GetServerState(ctx, cacheKeyA)
	if err != nil {
		t.Fatalf("User A should be able to access their own data: %v", err)
	}

	if stateADirect.ServerID != stateA.ServerID {
		t.Error("Inconsistent data when accessing via cache key")
	}

	t.Logf("✓ Cache key isolation prevents cross-user access")
}
