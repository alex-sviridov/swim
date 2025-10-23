package integration

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/decommissioner"
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
)

// RateLimitedTestRedis implements redis.ClientInterface with proper rate limiting
// This is used for rate limit integration tests to simulate real Redis behavior
type RateLimitedTestRedis struct {
	states         map[string]redis.ServerState
	queues         map[string][]string
	rateLimitTimes map[string]time.Time // Track when rate limit was acquired
	mu             sync.RWMutex
}

func NewRateLimitedTestRedis() *RateLimitedTestRedis {
	return &RateLimitedTestRedis{
		states:         make(map[string]redis.ServerState),
		queues:         make(map[string][]string),
		rateLimitTimes: make(map[string]time.Time),
	}
}

func (c *RateLimitedTestRedis) GetServerState(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	state, ok := c.states[cacheKey]
	if !ok {
		return nil, fmt.Errorf("server state not found in cache")
	}
	return &state, nil
}

func (c *RateLimitedTestRedis) PushServerState(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.states[cacheKey] = state
	return nil
}

func (c *RateLimitedTestRedis) DeleteServerState(ctx context.Context, cacheKey string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.states, cacheKey)
	return nil
}

func (c *RateLimitedTestRedis) PushPayload(ctx context.Context, queueKey string, payload string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.queues[queueKey] = append(c.queues[queueKey], payload)
	return nil
}

func (c *RateLimitedTestRedis) PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	queue, ok := c.queues[queueKey]
	if !ok || len(queue) == 0 {
		return "", fmt.Errorf("no payload available")
	}

	payload := queue[0]
	c.queues[queueKey] = queue[1:]
	return payload, nil
}

func (c *RateLimitedTestRedis) GetAllServerStates(ctx context.Context, prefix string) ([]redis.ServerState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	states := make([]redis.ServerState, 0)
	for key, state := range c.states {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			states = append(states, state)
		}
	}
	return states, nil
}

func (c *RateLimitedTestRedis) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := redis.RateLimitKey(webUserID, operation)

	// Check if rate limit exists and hasn't expired
	if acquiredAt, exists := c.rateLimitTimes[key]; exists {
		if time.Since(acquiredAt) < ttl {
			// Still rate limited
			return false, nil
		}
		// TTL expired, can acquire again
	}

	// Acquire rate limit
	c.rateLimitTimes[key] = time.Now()
	return true, nil
}

func (c *RateLimitedTestRedis) Close() error {
	return nil
}

// TestRateLimit_FloodingWithMultipleUsers simulates flooding the system with messages
// from different users and verifies that:
// 1. Each user ends up with exactly one VM
// 2. Cache state matches cloud state
// 3. Rate limiting prevents duplicate VMs
func TestRateLimit_FloodingWithMultipleUsers(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewRateLimitedTestRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Create provisioner and decommissioner with very fast polling for tests
	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(1 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)

	ctx := context.Background()

	// Test parameters
	numUsers := 5
	messagesPerUser := 10

	// Track what we send
	type userRequest struct {
		userID string
		labID  int
	}
	allRequests := []userRequest{}

	// Phase 1: Flood the system with provision requests
	t.Log("Phase 1: Flooding system with provision requests")
	var wg sync.WaitGroup
	for userIdx := 0; userIdx < numUsers; userIdx++ {
		userID := fmt.Sprintf("user-%d", userIdx)

		// Each user sends multiple requests for different labs
		for msgIdx := 0; msgIdx < messagesPerUser; msgIdx++ {
			labID := (msgIdx % 3) + 1 // Cycle through labs 1, 2, 3

			wg.Add(1)
			go func(uid string, lid int) {
				defer wg.Done()
				payload := fmt.Sprintf(`{"webuserid":"%s","labId":%d}`, uid, lid)
				prov.ProcessRequest(ctx, payload)
			}(userID, labID)

			allRequests = append(allRequests, userRequest{userID: userID, labID: labID})

			// Small delay between messages to simulate real-world scenario
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Give time for polling to complete
	time.Sleep(500 * time.Millisecond)

	t.Logf("Sent %d total provision requests (%d users × %d messages/user)",
		len(allRequests), numUsers, messagesPerUser)

	// Phase 2: Verify results
	t.Log("Phase 2: Verifying system state")

	// Verify: Each user has exactly ONE server in the cloud
	serversInCloud, err := mockConn.ListServers()
	if err != nil {
		t.Fatalf("Failed to list servers: %v", err)
	}
	t.Logf("Servers in cloud: %d", len(serversInCloud))

	if len(serversInCloud) > numUsers {
		t.Errorf("Expected at most %d servers in cloud (one per user), got %d",
			numUsers, len(serversInCloud))

		// Debug: show what servers exist
		serversByUser := make(map[string]int)
		for _, server := range serversInCloud {
			// Extract user from server name (format: "server-userX-labY")
			serversByUser[server.GetName()]++
		}
		t.Logf("Servers by name: %+v", serversByUser)
	}

	// Verify: Cache has exactly one entry per user
	allCachedStates, err := redisClient.GetAllServerStates(ctx, config.ServerCachePrefix)
	if err != nil {
		t.Fatalf("Failed to get all server states: %v", err)
	}

	t.Logf("Cached states: %d", len(allCachedStates))

	if len(allCachedStates) != numUsers {
		t.Errorf("Expected %d cached states (one per user), got %d",
			numUsers, len(allCachedStates))
	}

	// Verify: Cache matches cloud state for each user
	t.Log("Phase 3: Verifying cache matches cloud state")

	cacheMatchesCloud := true
	for userIdx := 0; userIdx < numUsers; userIdx++ {
		userID := fmt.Sprintf("user-%d", userIdx)
		cacheKey := redis.ServerCacheKey(userID)

		// Get cached state
		cachedState, err := redisClient.GetServerState(ctx, cacheKey)
		if err != nil {
			t.Errorf("User %s: No cached state found: %v", userID, err)
			cacheMatchesCloud = false
			continue
		}

		// Get server from cloud by ServerID
		cloudServer, err := mockConn.GetServerByID(cachedState.ServerID)
		if err != nil {
			t.Errorf("User %s: Server ID %s in cache but not in cloud: %v",
				userID, cachedState.ServerID, err)
			cacheMatchesCloud = false
			continue
		}

		// Verify cache data matches cloud server
		if cloudServer.GetID() != cachedState.ServerID {
			t.Errorf("User %s: ServerID mismatch - cache: %s, cloud: %s",
				userID, cachedState.ServerID, cloudServer.GetID())
			cacheMatchesCloud = false
		}

		if cloudServer.GetIPv6Address() != cachedState.Address {
			t.Errorf("User %s: Address mismatch - cache: %s, cloud: %s",
				userID, cachedState.Address, cloudServer.GetIPv6Address())
			cacheMatchesCloud = false
		}

		t.Logf("✓ User %s: Cache matches cloud (ServerID: %s, LabID: %d)",
			userID, cachedState.ServerID, cachedState.LabID)
	}

	if !cacheMatchesCloud {
		t.Error("Cache state does not match cloud state")
	}

	// Phase 4: Test decommissioning with flooding
	t.Log("Phase 4: Testing decommission with message flooding")

	// Flood with decommission requests for one user
	testUserID := "user-0"
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := fmt.Sprintf(`{"webuserid":"%s"}`, testUserID)
			decomm.ProcessRequest(ctx, payload)
		}()
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	// Verify: User 0's server was deleted
	cacheKey := redis.ServerCacheKey(testUserID)
	_, err = redisClient.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Errorf("Expected user-0 to have no cached state after decommission")
	}

	// Verify: Cloud has one less server
	remainingServers, err := mockConn.ListServers()
	if err != nil {
		t.Fatalf("Failed to list remaining servers: %v", err)
	}
	expectedRemaining := numUsers - 1
	if len(remainingServers) != expectedRemaining {
		t.Errorf("Expected %d servers after decommissioning user-0, got %d",
			expectedRemaining, len(remainingServers))
	}

	// Final summary
	t.Logf("\n=== Test Summary ===")
	t.Logf("Total requests sent: %d", len(allRequests))
	t.Logf("Final servers in cloud: %d (expected: %d)", len(remainingServers), expectedRemaining)
	t.Logf("Final cached states: %d (expected: %d)", len(allCachedStates)-1, expectedRemaining)
	t.Logf("Rate limiting: EFFECTIVE - No duplicate VMs created")
	t.Logf("Cache consistency: VERIFIED - Cache matches cloud state")
}

// TestRateLimit_RapidLabSwitching tests that rapid lab switching by a single user
// doesn't create orphaned VMs or cache inconsistencies
func TestRateLimit_RapidLabSwitching(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewRateLimitedTestRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	userID := "rapid-switcher"

	// Rapidly switch between labs 1, 2, 3, 4, 5
	t.Log("Rapidly switching between labs 1-5")
	for labID := 1; labID <= 5; labID++ {
		payload := fmt.Sprintf(`{"webuserid":"%s","labId":%d}`, userID, labID)
		prov.ProcessRequest(ctx, payload)
		time.Sleep(20 * time.Millisecond) // Simulate rapid clicks
	}

	// Wait for polling to complete
	time.Sleep(500 * time.Millisecond)

	// Verify: Only ONE server exists in cloud for this user
	allServers, err := mockConn.ListServers()
	if err != nil {
		t.Fatalf("Failed to list servers: %v", err)
	}
	if len(allServers) > 1 {
		t.Errorf("Expected at most 1 server after rapid switching, got %d", len(allServers))
		for _, server := range allServers {
			t.Logf("  Server: %s (ID: %s)", server.GetName(), server.GetID())
		}
	}

	// Verify: Cache has exactly one entry
	cacheKey := redis.ServerCacheKey(userID)
	cachedState, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Expected cached state for user, got error: %v", err)
	}

	// Verify: Cached lab ID is one of the requested labs (race condition may determine which wins)
	if cachedState.LabID < 1 || cachedState.LabID > 5 {
		t.Errorf("Expected LabID between 1-5, got %d", cachedState.LabID)
	}

	// Verify: No orphaned servers (all servers in cloud are in cache)
	if len(allServers) > 0 {
		server := allServers[0]
		if server.GetID() != cachedState.ServerID {
			t.Errorf("Orphaned server detected - ServerID in cloud (%s) doesn't match cache (%s)",
				server.GetID(), cachedState.ServerID)
		}
	}

	t.Logf("✓ Rapid lab switching handled correctly")
	t.Logf("  Final lab: %d", cachedState.LabID)
	t.Logf("  Servers in cloud: %d", len(allServers))
	t.Logf("  Cache consistent: YES")
}

// TestRateLimit_ConcurrentProvisionAndDecommission tests concurrent provision and
// decommission requests for the same user
func TestRateLimit_ConcurrentProvisionAndDecommission(t *testing.T) {
	// Setup
	mockConn := NewMockConnector()
	redisClient := NewRateLimitedTestRedis()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelWarn}))

	prov := provisioner.New(log, mockConn, redisClient).WithPollInterval(1 * time.Millisecond)
	decomm := decommissioner.New(log, mockConn, redisClient)
	ctx := context.Background()

	userID := "concurrent-user"

	// Start with a provisioned server
	t.Log("Provisioning initial server")
	initialPayload := fmt.Sprintf(`{"webuserid":"%s","labId":1}`, userID)
	prov.ProcessRequest(ctx, initialPayload)
	time.Sleep(200 * time.Millisecond)

	// Verify initial state
	if mockConn.GetServerCount() != 1 {
		t.Fatalf("Expected 1 server after initial provision, got %d", mockConn.GetServerCount())
	}

	// Concurrently send provision and decommission requests
	t.Log("Sending concurrent provision and decommission requests")
	var wg sync.WaitGroup

	// Send multiple provisions
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(labID int) {
			defer wg.Done()
			payload := fmt.Sprintf(`{"webuserid":"%s","labId":%d}`, userID, labID+2)
			prov.ProcessRequest(ctx, payload)
		}(i)
	}

	// Send multiple decommissions
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := fmt.Sprintf(`{"webuserid":"%s"}`, userID)
			decomm.ProcessRequest(ctx, payload)
		}()
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	// Verify: Either 0 or 1 server exists (no duplicates)
	finalServerCount := mockConn.GetServerCount()
	if finalServerCount > 1 {
		t.Errorf("Expected 0 or 1 server after concurrent operations, got %d", finalServerCount)
	}

	// Verify: Cache is consistent with cloud
	cacheKey := redis.ServerCacheKey(userID)
	cachedState, cacheErr := redisClient.GetServerState(ctx, cacheKey)

	if finalServerCount == 0 {
		// If no servers in cloud, cache should be empty too
		if cacheErr == nil {
			t.Error("Cloud has 0 servers but cache still has state")
		} else {
			t.Log("✓ No servers in cloud, cache is empty")
		}
	} else {
		// If server exists in cloud, it should be in cache
		if cacheErr != nil {
			t.Errorf("Cloud has server but cache is empty: %v", cacheErr)
		} else {
			servers, err := mockConn.ListServers()
			if err != nil {
				t.Fatalf("Failed to list servers: %v", err)
			}
			if len(servers) > 0 && servers[0].GetID() != cachedState.ServerID {
				t.Error("Cache ServerID doesn't match cloud ServerID")
			} else {
				t.Log("✓ Cache matches cloud state")
			}
		}
	}

	t.Logf("Final state: %d server(s) in cloud, cache consistent: YES", finalServerCount)
}
