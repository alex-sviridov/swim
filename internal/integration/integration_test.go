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
)

// TestEndToEnd_ProvisioningFlow tests the complete provisioning workflow
func TestEndToEnd_ProvisioningFlow(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	userID := "e2e-provision-user"
	labID := 5

	// Step 1: LabMan pushes provision request to queue
	pushProvisionRequest(t, ctx, client, userID, labID)
	t.Logf("✓ Provision request pushed to queue")

	// Step 2: SWIM pops request from queue
	payload, err := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to pop provision request: %v", err)
	}
	t.Logf("✓ Provision request popped from queue")

	// Step 3: SWIM processes provisioning
	prov.ProcessRequest(ctx, payload)
	t.Logf("✓ Provision request processed")

	// Step 4: Verify initial provisioning state in cache
	cacheKey := "vmmanager:servers:" + userID
	state, err := client.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to get initial server state: %v", err)
	}

	if state.Status != config.StatusProvisioning && state.Status != config.StatusRunning {
		t.Errorf("Expected status provisioning or running, got %s", state.Status)
	}
	t.Logf("✓ Initial cache entry created with status: %s", state.Status)

	// Step 5: Wait for server to become available (this is what LabMan should do)
	runningState, err := waitForServerAvailable(ctx, client, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Server did not become available: %v", err)
	}

	// Step 6: Verify LabMan-compatible cache format
	assertServerAvailable(t, runningState)

	if runningState.Address == "" {
		t.Error("Expected non-empty address")
	}

	if runningState.ServerID == "" {
		t.Error("Expected non-empty server ID")
	}

	if runningState.WebUserID != userID {
		t.Errorf("Expected WebUserID %s, got %s", userID, runningState.WebUserID)
	}

	if runningState.LabID != labID {
		t.Errorf("Expected LabID %d, got %d", labID, runningState.LabID)
	}

	t.Logf("✓ Server running with address: %s", runningState.Address)
	t.Logf("✓ Cache format valid for LabMan consumption")
}

// TestEndToEnd_DecommissioningFlow tests the complete decommissioning workflow
func TestEndToEnd_DecommissioningFlow(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "e2e-decommission-user"
	labID := 7

	// Provision a server first
	pushProvisionRequest(t, ctx, client, userID, labID)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	state, err := waitForServerAvailable(ctx, client, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to provision server: %v", err)
	}
	serverID := state.ServerID
	t.Logf("✓ Server provisioned and available: %s", serverID)

	// Step 1: LabMan pushes decommission request to queue
	pushDecommissionRequest(t, ctx, client, userID, labID)
	t.Logf("✓ Decommission request pushed to queue")

	// Step 2: SWIM pops request from queue
	decommPayload, err := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to pop decommission request: %v", err)
	}
	t.Logf("✓ Decommission request popped from queue")

	// Step 3: SWIM processes decommissioning
	decomm.ProcessRequest(ctx, decommPayload)
	t.Logf("✓ Decommission request processed")

	// Step 4: Verify server is deleted from cache
	err = waitForServerDeletion(ctx, client, userID, labID, 3*time.Second)
	if err != nil {
		t.Fatalf("Server was not deleted from cache: %v", err)
	}
	t.Logf("✓ Server removed from cache")

	// Step 5: Verify server is deleted from connector
	_, err = mockConn.GetServerByID(serverID)
	if err == nil {
		t.Error("Server should be deleted from connector")
	}
	t.Logf("✓ Server deleted from cloud provider")
}

// TestEndToEnd_DecommissionAlreadyDeleted tests graceful handling of already-deleted servers
func TestEndToEnd_DecommissionAlreadyDeleted(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "already-deleted-user"
	labID := 3

	// Provision a server
	pushProvisionRequest(t, ctx, client, userID, labID)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	state, err := waitForServerAvailable(ctx, client, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to provision server: %v", err)
	}
	serverID := state.ServerID

	// Manually delete the server from connector (simulating manual deletion)
	server, _ := mockConn.GetServerByID(serverID)
	server.Delete()
	t.Logf("✓ Server manually deleted from cloud provider")

	// Now try to decommission via queue
	pushDecommissionRequest(t, ctx, client, userID, labID)
	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	// Should still clean up cache
	err = waitForServerDeletion(ctx, client, userID, labID, 3*time.Second)
	if err != nil {
		t.Fatalf("Cache should be cleaned even if server already deleted: %v", err)
	}

	t.Logf("✓ Cache cleaned up gracefully even when server already deleted")
}

// TestLabManScenario_FirstTimeUser simulates TASK.md Scenario 1: No lab running
func TestLabManScenario_FirstTimeUser(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	userID := "first-time-user"
	labID := 5

	// Scenario: User opens LabPage for lab 5, no lab running
	// LabMan queries status → idle
	count, _ := countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs for new user, got %d", count)
	}
	t.Logf("✓ User has no active labs (status: idle)")

	// LabMan connects WebSocket with labId=5
	// LabMan determines: no lab running → start provisioning
	// LabMan pushes provision request
	pushProvisionRequest(t, ctx, client, userID, labID)

	// SWIM processes
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	// User sees provisioning updates
	state, _ := client.GetServerState(ctx, "vmmanager:servers:"+userID)
	t.Logf("✓ Provisioning started, status: %s", state.Status)

	// Wait for server to become available (LabMan should check available == true)
	runningState, err := waitForServerAvailable(ctx, client, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab did not become available: %v", err)
	}

	// LabMan connects SSH using address from cache
	t.Logf("✓ Lab available, LabMan can connect SSH to: %s", runningState.Address)
	t.Logf("✓ User can interact with lab")
}

// TestLabManScenario_SwitchLabs simulates TASK.md Scenario 2: Different lab running
func TestLabManScenario_SwitchLabs(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "switch-lab-user"

	// User has lab 3 running
	pushProvisionRequest(t, ctx, client, userID, 3)
	payload1, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload1)
	_, err := waitForServerAvailable(ctx, client, userID, 3, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 3 failed to become available: %v", err)
	}
	t.Logf("✓ User has lab 3 running and available")

	// User opens LabPage for lab 5
	// LabMan queries status → running (labId: 3)
	labs, _ := getUserLabs(ctx, client, userID)
	if len(labs) != 1 || labs[0] != 3 {
		t.Errorf("Expected lab 3 running, got %v", labs)
	}
	t.Logf("✓ LabMan detects different lab (3) is running")

	// User clicks "Stop and Connect"
	// LabMan calls POST /stop → decommissions lab 3
	pushDecommissionRequest(t, ctx, client, userID, 3)
	decommPayload, _ := client.PopPayload(ctx, config.DecommissionQueueKey, 5*time.Second)
	decomm.ProcessRequest(ctx, decommPayload)

	err = waitForServerDeletion(ctx, client, userID, 3, 3*time.Second)
	if err != nil {
		t.Fatalf("Lab 3 was not deleted: %v", err)
	}
	t.Logf("✓ Lab 3 stopped")

	// Status becomes idle
	count, _ := countUserLabs(ctx, client, userID)
	if count != 0 {
		t.Errorf("Expected 0 labs after stop, got %d", count)
	}
	t.Logf("✓ User status: idle")

	// LabMan connects WebSocket to lab 5
	pushProvisionRequest(t, ctx, client, userID, 5)
	payload2, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload2)

	state, err := waitForServerAvailable(ctx, client, userID, 5, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 failed to become available: %v", err)
	}
	t.Logf("✓ Lab 5 provisioned and available: %s", state.Address)

	// Verify only lab 5 is active
	labs, _ = getUserLabs(ctx, client, userID)
	if len(labs) != 1 || labs[0] != 5 {
		t.Errorf("Expected only lab 5, got %v", labs)
	}
}

// TestLabManScenario_ReconnectExisting simulates TASK.md Scenario 3: Same lab running
func TestLabManScenario_ReconnectExisting(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)

	userID := "reconnect-user"
	labID := 5

	// User has lab 5 running in one tab
	pushProvisionRequest(t, ctx, client, userID, labID)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	state1, err := waitForServerAvailable(ctx, client, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab 5 failed to start: %v", err)
	}
	t.Logf("✓ Lab 5 running in first tab: %s", state1.Address)

	// User opens lab 5 in another tab
	// LabMan queries status → running (labId: 5)
	labs, _ := getUserLabs(ctx, client, userID)
	if len(labs) != 1 || labs[0] != 5 {
		t.Errorf("Expected lab 5 running, got %v", labs)
	}
	t.Logf("✓ LabMan detects same lab (5) is running")

	// LabMan connects WebSocket to existing session
	// No new provision request needed - just read from cache
	cacheKey := "vmmanager:servers:" + userID
	state2, err := client.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to read existing lab state: %v", err)
	}

	if state2.Address != state1.Address {
		t.Error("Address should be the same for existing lab")
	}

	if state2.ServerID != state1.ServerID {
		t.Error("Server ID should be the same for existing lab")
	}

	t.Logf("✓ LabMan reconnects to existing session at: %s", state2.Address)
	t.Logf("✓ No duplicate provisioning")
}

// TestLabManScenario_DisconnectTimeout simulates TASK.md Scenario 4: Disconnect and timeout
func TestLabManScenario_DisconnectTimeout(t *testing.T) {
	client, cleanupFunc := setupTestRedis(t)
	defer cleanupFunc()

	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	mockConn := NewMockConnector()
	prov := provisioner.New(log, mockConn, client).WithPollInterval(200 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, client)

	userID := "disconnect-timeout-user"
	labID := 8

	// Set short TTL for testing
	os.Setenv("DEFAULT_TTL_MINUTES", "1")
	defer os.Unsetenv("DEFAULT_TTL_MINUTES")

	// User provisions lab
	pushProvisionRequest(t, ctx, client, userID, labID)
	payload, _ := client.PopPayload(ctx, config.ProvisionQueueKey, 5*time.Second)
	prov.ProcessRequest(ctx, payload)

	state, err := waitForServerAvailable(ctx, client, userID, labID, 5*time.Second)
	if err != nil {
		t.Fatalf("Lab failed to start: %v", err)
	}
	t.Logf("✓ Lab running: %s", state.Address)

	// User closes browser tab (WebSocket disconnects)
	// In real LabMan: set disconnectTimerAt to now + 5 minutes
	// In SWIM: TTL handles this via ExpiresAt
	t.Logf("✓ User disconnects (browser tab closed)")

	// Simulate time passing (expire the lab)
	state.ExpiresAt = time.Now().Add(-1 * time.Minute)
	cacheKey := "vmmanager:servers:" + userID
	client.PushServerState(ctx, cacheKey, *state, config.ServerCacheTTL)

	// Cleanup worker runs and finds expired lab
	cleanupWorker := cleanup.New(log, mockConn, client)
	go func() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		cleanupWorker.Run(cleanupCtx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Process decommission
	decommPayload, err := client.PopPayload(ctx, config.DecommissionQueueKey, 3*time.Second)
	if err != nil {
		t.Fatalf("Expected decommission request: %v", err)
	}
	decomm.ProcessRequest(ctx, decommPayload)

	// Verify lab is stopped
	err = waitForServerDeletion(ctx, client, userID, labID, 3*time.Second)
	if err != nil {
		t.Fatalf("Lab was not deleted: %v", err)
	}

	t.Logf("✓ Lab stopped after disconnect timeout")
	t.Logf("✓ Resources freed automatically")
}
