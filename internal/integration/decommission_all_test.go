package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/decommissioner"
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
)

// TestDecommissionAll_SingleLab tests decommissioning all labs when user has one lab
func TestDecommissionAll_SingleLab(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "decom-all-single-user"

	// Provision one lab
	pushProvisionRequest(t, ctx, client, userID, 5)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	_, err := waitForServerAvailable(ctx, client, userID, 5, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 failed to start: %v", err)
	}

	// Verify user has 1 lab
	count, _ := countUserLabs(ctx, client, userID)
	if count != 1 {
		t.Errorf("Expected 1 lab, got %d", count)
	}

	// Decommission all labs (without specifying labId)
	req := map[string]interface{}{
		"webuserid": userID,
		// labId intentionally omitted
	}
	decommPayload2, _ := json.Marshal(req)
	_ = client.PushPayload(ctx, config.DecommissionQueueKey, string(decommPayload2))

	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	// Verify all labs are deleted
	err = waitForServerDeletion(ctx, client, userID, 5, 3*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 was not deleted: %v", err)
	}

	count, _ = countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs after decommission all, got %d", count)
	}

	// Verify VM was deleted
	serverCount := mockConn.GetServerCount()
	if serverCount != 0 {
		t.Errorf("Expected 0 servers, got %d", serverCount)
	}

	t.Logf("✓ Decommission all deleted single lab successfully")
}

// TestDecommissionAll_LabSwitching tests that decommission without labId works when user switches labs
// This validates the "one lab per user" architecture - provisioning a new lab overwrites the old one
func TestDecommissionAll_LabSwitching(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	// Set short rate limits for testing to avoid timeouts
	_ = os.Setenv("PROVISION_RATE_LIMIT_SECONDS", "1")
	_ = os.Setenv("DECOMMISSION_RATE_LIMIT_SECONDS", "1")
	defer func() { _ = os.Unsetenv("PROVISION_RATE_LIMIT_SECONDS") }()
	defer func() { _ = os.Unsetenv("DECOMMISSION_RATE_LIMIT_SECONDS") }()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "decom-all-multi-user"

	// Provision lab 1
	pushProvisionRequest(t, ctx, client, userID, 1)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	_, err := waitForServerAvailable(ctx, client, userID, 1, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 1 failed to start: %v", err)
	}

	// Wait for provision rate limit to expire before provisioning lab 2
	time.Sleep(1100 * time.Millisecond)

	// Provision lab 2 (overwrites lab 1 in cache per "one lab per user" rule)
	pushProvisionRequest(t, ctx, client, userID, 2)
	payload, _ = client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	_, err = waitForServerAvailable(ctx, client, userID, 2, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 2 failed to start: %v", err)
	}

	// Verify only 1 lab exists in cache (lab 2, which overwrote lab 1)
	count, _ := countUserLabs(ctx, client, userID)
	if count != 1 {
		t.Errorf("Expected 1 lab (due to overwrite), got %d", count)
	}

	// Verify 2 VMs exist in the cloud (old one wasn't cleaned up yet)
	initialServerCount := mockConn.GetServerCount()
	if initialServerCount != 2 {
		t.Errorf("Expected 2 servers in cloud, got %d", initialServerCount)
	}

	// First, process the automatic decommission request for lab 1 that was queued during provisioning
	autoDecommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, autoDecommPayload)

	// Decommission without labId (should decommission lab 2, the current lab)
	req := map[string]interface{}{
		"webuserid": userID,
	}
	reqPayload, _ := json.Marshal(req)
	_ = client.PushPayload(ctx, config.DecommissionQueueKey, string(reqPayload))

	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	// Wait for decommission
	time.Sleep(1 * time.Second)

	// Verify cache is cleared
	count, _ = countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs after decommission all, got %d", count)
	}

	// Verify both labs were deleted (0 servers should remain)
	serverCount := mockConn.GetServerCount()
	if serverCount != 0 {
		t.Errorf("Expected 0 servers remaining (both labs deleted), got %d", serverCount)
	}

	t.Logf("✓ Decommission without labId correctly decommissioned current lab")
}

// TestDecommissionAll_NoLabs tests decommissioning all labs when user has no labs
func TestDecommissionAll_NoLabs(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	decomm := decommissioner.New(log, mockConn, client)

	userID := "decom-all-no-labs-user"

	// Verify user has no labs
	count, _ := countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs, got %d", count)
	}

	// Decommission all labs (should be no-op)
	req := map[string]interface{}{
		"webuserid": userID,
	}
	payload, _ := json.Marshal(req)
	_ = client.PushPayload(ctx, config.DecommissionQueueKey, string(payload))

	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	// Should complete without error
	count, _ = countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs, got %d", count)
	}

	t.Logf("✓ Decommission all with no labs handled gracefully")
}

// TestDecommissionAll_UserIsolation tests that decommission all only affects the specified user
// Each user can only have 1 lab at a time per the architecture
func TestDecommissionAll_UserIsolation(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userA := "decom-all-userA"
	userB := "decom-all-userB"

	// User A provisions lab 1
	pushProvisionRequest(t, ctx, client, userA, 1)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)
	_, _ = waitForServerAvailable(ctx, client, userA, 1, 5*time.Second)

	// User B provisions lab 2
	pushProvisionRequest(t, ctx, client, userB, 2)
	payload, _ = client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)
	_, _ = waitForServerAvailable(ctx, client, userB, 2, 5*time.Second)

	// Verify both users have 1 lab each
	countA, _ := countUserLabs(ctx, client, userA)
	countB, _ := countUserLabs(ctx, client, userB)
	if countA != 1 {
		t.Errorf("Expected User A to have 1 lab, got %d", countA)
	}
	if countB != 1 {
		t.Errorf("Expected User B to have 1 lab, got %d", countB)
	}

	// Decommission User A's lab only
	req := map[string]interface{}{
		"webuserid": userA,
	}
	reqPayload, _ := json.Marshal(req)
	_ = client.PushPayload(ctx, config.DecommissionQueueKey, string(reqPayload))

	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	time.Sleep(1 * time.Second)

	// Verify User A has no labs
	countA, _ = countUserLabs(ctx, client, userA)
	if countA != 0 {
		t.Errorf("Expected User A to have 0 labs, got %d", countA)
	}

	// Verify User B still has their lab
	countB, _ = countUserLabs(ctx, client, userB)
	if countB != 1 {
		t.Errorf("Expected User B to still have 1 lab, got %d", countB)
	}

	// Verify User B's lab is still accessible and available
	cacheKey := redis.ServerCacheKey(userB)
	state, err := client.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Errorf("User B's lab should still exist: %v", err)
	} else {
		if !state.Available {
			t.Errorf("User B's lab should be available, got available=%v, status=%s", state.Available, state.Status)
		}
		if state.LabID != 2 {
			t.Errorf("Expected User B's lab to be lab 2, got %d", state.LabID)
		}
	}

	t.Logf("✓ Decommission all respects user isolation")
}

// TestDecommissionAll_VsSpecificLab tests decommissioning with and without labId
// In the one-lab-per-user architecture, user can only have 1 lab at a time
func TestDecommissionAll_VsSpecificLab(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "decom-comparison-user"

	// Provision lab 1
	pushProvisionRequest(t, ctx, client, userID, 1)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)
	_, _ = waitForServerAvailable(ctx, client, userID, 1, 5*time.Second)

	count, _ := countUserLabs(ctx, client, userID)
	if count != 1 {
		t.Errorf("Expected 1 lab, got %d", count)
	}

	// Test 1: Decommission with specific labId
	pushDecommissionRequest(t, ctx, client, userID, 1)
	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	time.Sleep(500 * time.Millisecond)

	// Verify lab is deleted
	count, _ = countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs after specific decommission, got %d", count)
	}

	// Provision lab 2
	pushProvisionRequest(t, ctx, client, userID, 2)
	payload, _ = client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)
	_, _ = waitForServerAvailable(ctx, client, userID, 2, 5*time.Second)

	count, _ = countUserLabs(ctx, client, userID)
	if count != 1 {
		t.Errorf("Expected 1 lab, got %d", count)
	}

	// Test 2: Decommission without labId (decommission all)
	req := map[string]interface{}{
		"webuserid": userID,
	}
	reqPayload, _ := json.Marshal(req)
	_ = client.PushPayload(ctx, config.DecommissionQueueKey, string(reqPayload))

	decommPayload, _ = client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	time.Sleep(1 * time.Second)

	// Verify lab is deleted
	count, _ = countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs after decommission all, got %d", count)
	}

	t.Logf("✓ Both specific and decommission-all methods work correctly")
}
