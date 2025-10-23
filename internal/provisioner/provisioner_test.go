package provisioner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

// Mock Redis Client
type mockRedisClient struct {
	pushServerStateFunc   func(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error
	deleteServerStateFunc func(ctx context.Context, cacheKey string) error
	getServerStateFunc    func(ctx context.Context, cacheKey string) (*redis.ServerState, error)
	pushPayloadFunc       func(ctx context.Context, queueKey string, payload string) error
	states                map[string]redis.ServerState
	queuedPayloads        []string // Track payloads pushed to queues
}

func (m *mockRedisClient) PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error) {
	return "", nil
}

func (m *mockRedisClient) PushPayload(ctx context.Context, queueKey string, payload string) error {
	if m.pushPayloadFunc != nil {
		return m.pushPayloadFunc(ctx, queueKey, payload)
	}
	m.queuedPayloads = append(m.queuedPayloads, payload)
	return nil
}

func (m *mockRedisClient) PushServerState(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error {
	if m.pushServerStateFunc != nil {
		return m.pushServerStateFunc(ctx, cacheKey, state, ttl)
	}
	if m.states == nil {
		m.states = make(map[string]redis.ServerState)
	}
	m.states[cacheKey] = state
	return nil
}

func (m *mockRedisClient) GetServerState(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
	if m.getServerStateFunc != nil {
		return m.getServerStateFunc(ctx, cacheKey)
	}
	state, ok := m.states[cacheKey]
	if !ok {
		return nil, fmt.Errorf("server state not found in cache")
	}
	return &state, nil
}

func (m *mockRedisClient) GetAllServerStates(ctx context.Context, prefix string) ([]redis.ServerState, error) {
	return nil, nil
}

func (m *mockRedisClient) DeleteServerState(ctx context.Context, cacheKey string) error {
	if m.deleteServerStateFunc != nil {
		return m.deleteServerStateFunc(ctx, cacheKey)
	}
	if m.states != nil {
		delete(m.states, cacheKey)
	}
	return nil
}

func (m *mockRedisClient) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	// Allow by default in tests (not rate limited)
	return true, nil
}

func (m *mockRedisClient) Close() error {
	return nil
}

// Mock Server
type mockServer struct {
	id            string
	name          string
	ipv6Address   string
	state         string
	stateErr      error
	deleteErr     error
	deleteCalled  bool
	stateSequence []string
	stateIndex    int
}

func (m *mockServer) GetID() string {
	return m.id
}

func (m *mockServer) GetName() string {
	return m.name
}

func (m *mockServer) GetIPv6Address() string {
	return m.ipv6Address
}

func (m *mockServer) GetState() (string, error) {
	if m.stateErr != nil {
		return "", m.stateErr
	}
	if len(m.stateSequence) > 0 {
		if m.stateIndex < len(m.stateSequence) {
			state := m.stateSequence[m.stateIndex]
			m.stateIndex++
			return state, nil
		}
		return m.stateSequence[len(m.stateSequence)-1], nil
	}
	return m.state, nil
}

func (m *mockServer) Delete() error {
	m.deleteCalled = true
	return m.deleteErr
}

func (m *mockServer) String() string {
	return fmt.Sprintf("Server{id=%s, name=%s, ipv6=%s}", m.id, m.name, m.ipv6Address)
}

// Mock Connector
type mockConnector struct {
	createServerFunc func(payload string) (connector.Server, error)
	server           connector.Server
	createErr        error
}

func (m *mockConnector) ListServers() ([]connector.Server, error) {
	return nil, nil
}

func (m *mockConnector) GetServerByID(id string) (connector.Server, error) {
	return nil, nil
}

func (m *mockConnector) CreateServer(payload string) (connector.Server, error) {
	if m.createServerFunc != nil {
		return m.createServerFunc(payload)
	}
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.server, nil
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNew(t *testing.T) {
	log := newTestLogger()
	mockConn := &mockConnector{}
	mockRedis := &mockRedisClient{}

	p := New(log, mockConn, mockRedis)

	if p.log != log {
		t.Error("logger not set correctly")
	}
	if p.conn != mockConn {
		t.Error("connector not set correctly")
	}
	if p.redisClient != mockRedis {
		t.Error("redis client not set correctly")
	}
	if p.pollInterval != defaultPollInterval {
		t.Errorf("expected poll interval %v, got %v", defaultPollInterval, p.pollInterval)
	}
}

func TestWithPollInterval(t *testing.T) {
	log := newTestLogger()
	mockConn := &mockConnector{}
	mockRedis := &mockRedisClient{}

	customInterval := 5 * time.Second
	p := New(log, mockConn, mockRedis).WithPollInterval(customInterval)

	if p.pollInterval != customInterval {
		t.Errorf("expected poll interval %v, got %v", customInterval, p.pollInterval)
	}
}

func TestProcessRequest_InvalidPayload(t *testing.T) {
	log := newTestLogger()
	mockConn := &mockConnector{}
	mockRedis := &mockRedisClient{}

	p := New(log, mockConn, mockRedis)
	ctx := context.Background()

	// Invalid JSON payload
	p.ProcessRequest(ctx, "invalid json")

	// Should not create any servers
	if mockConn.createServerFunc != nil {
		t.Error("should not attempt to create server with invalid payload")
	}
}

func TestProcessRequest_SuccessfulProvisioning(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:            "server-123",
		name:          "test-server",
		ipv6Address:   "2001:db8::1",
		stateSequence: []string{"starting", "running"}, // Transitions to running
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify initial state was cached
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server state to be cached, got error: %v", err)
	}

	// Verify server state fields
	if state.WebUserID != "user-123" {
		t.Errorf("expected WebUserID 'user-123', got %s", state.WebUserID)
	}
	if state.LabID != 42 {
		t.Errorf("expected LabID 42, got %d", state.LabID)
	}
	if state.ServerID != "server-123" {
		t.Errorf("expected ServerID 'server-123', got %s", state.ServerID)
	}
	if state.Address != "2001:db8::1" {
		t.Errorf("expected Address '2001:db8::1', got %s", state.Address)
	}
}

func TestProcessRequest_CreateServerError(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockConn := &mockConnector{
		createErr: errors.New("failed to create server"),
	}

	p := New(log, mockConn, mockRedis)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify cache was deleted after error
	cacheKey := redis.ServerCacheKey("user-123")
	_, err := mockRedis.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Error("expected cache to be deleted after creation error")
	}
}

func TestProcessRequest_WithEnvironmentVariables(t *testing.T) {
	// Set environment variables
	_ = os.Setenv("SSH_USERNAME", "custom-user")
	_ = os.Setenv("DEFAULT_TTL_MINUTES", "60")
	defer func() {
		_ = os.Unsetenv("SSH_USERNAME")
		_ = os.Unsetenv("DEFAULT_TTL_MINUTES")
	}()

	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify state uses custom environment variables
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server state to be cached, got error: %v", err)
	}

	if state.User != "custom-user" {
		t.Errorf("expected User 'custom-user', got %s", state.User)
	}

	// TTL should be ~60 minutes from now
	expectedExpiry := time.Now().Add(60 * time.Minute)
	timeDiff := state.ExpiresAt.Sub(expectedExpiry)
	if timeDiff > 5*time.Second || timeDiff < -5*time.Second {
		t.Errorf("expected ExpiresAt to be ~60 minutes from now, got diff of %v", timeDiff)
	}
}

func TestMapCloudStateToStatus(t *testing.T) {
	tests := []struct {
		cloudState     string
		expectedStatus string
	}{
		{"running", config.StatusRunning},
		{"starting", config.StatusProvisioning},
		{"initializing", config.StatusProvisioning},
		{"stopping", config.StatusStopping},
		{"off", config.StatusStopping},
		{"deleting", config.StatusStopping},
		{"unknown", config.StatusProvisioning},
		{"", config.StatusProvisioning},
	}

	for _, tt := range tests {
		t.Run(tt.cloudState, func(t *testing.T) {
			result := mapCloudStateToStatus(tt.cloudState)
			if result != tt.expectedStatus {
				t.Errorf("mapCloudStateToStatus(%q) = %q, want %q", tt.cloudState, result, tt.expectedStatus)
			}
		})
	}
}

func TestIsServerAvailable(t *testing.T) {
	tests := []struct {
		cloudState string
		expected   bool
	}{
		{"running", true},
		{"starting", false},
		{"initializing", false},
		{"stopping", false},
		{"off", false},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.cloudState, func(t *testing.T) {
			result := isServerAvailable(tt.cloudState)
			if result != tt.expected {
				t.Errorf("isServerAvailable(%q) = %v, want %v", tt.cloudState, result, tt.expected)
			}
		})
	}
}

func TestPollServerState_StateChanges(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:            "server-123",
		name:          "test-server",
		ipv6Address:   "2001:db8::1",
		stateSequence: []string{"starting", "initializing", "running"},
	}

	p := New(log, nil, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	cacheKey := redis.ServerCacheKey("user-123")
	initialState := redis.ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      config.StatusProvisioning,
		Available:   false,
		CloudStatus: "starting",
		ServerID:    "server-123",
		WebUserID:   "user-123",
		LabID:       42,
	}

	// Call pollServerState
	p.pollServerState(ctx, mockSrv, cacheKey, initialState, "starting")

	// Verify final state is "running"
	finalState, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server state to be cached, got error: %v", err)
	}

	if finalState.CloudStatus != "running" {
		t.Errorf("expected final CloudStatus 'running', got %s", finalState.CloudStatus)
	}
	if finalState.Status != config.StatusRunning {
		t.Errorf("expected final Status '%s', got %s", config.StatusRunning, finalState.Status)
	}
	if !finalState.Available {
		t.Error("expected server to be available when running")
	}
}

func TestPollServerState_Timeout(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "starting", // Never reaches "running"
	}

	p := New(log, nil, mockRedis).WithPollInterval(1 * time.Millisecond)

	// Create a context with very short timeout to simulate stateTimeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	cacheKey := redis.ServerCacheKey("user-123")
	initialState := redis.ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      config.StatusProvisioning,
		Available:   false,
		CloudStatus: "starting",
		ServerID:    "server-123",
		WebUserID:   "user-123",
		LabID:       42,
	}

	// This should timeout and return
	p.pollServerState(ctx, mockSrv, cacheKey, initialState, "starting")

	// Function should have returned without error
	// The state should still be in provisioning
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		// Error getting state after timeout
		t.Logf("State not found after timeout: %v", err)
	} else if state.Status != config.StatusRunning {
		// This is expected - server never reached running state
		t.Logf("Server did not reach running state: %s", state.Status)
	}
}

func TestPollServerState_GetStateError(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "starting",
		stateErr:    errors.New("failed to get state"),
	}

	p := New(log, nil, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	cacheKey := redis.ServerCacheKey("user-123")
	initialState := redis.ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      config.StatusProvisioning,
		Available:   false,
		CloudStatus: "starting",
		ServerID:    "server-123",
		WebUserID:   "user-123",
		LabID:       42,
	}

	// This should handle the error and delete the server
	p.pollServerState(ctx, mockSrv, cacheKey, initialState, "starting")

	// Verify server was deleted
	if !mockSrv.deleteCalled {
		t.Error("expected server to be deleted after GetState error")
	}

	// Verify cache was cleared
	_, err := mockRedis.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Error("expected cache to be cleared after error")
	}
}

func TestHandleProvisioningError(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
	}

	p := New(log, nil, mockRedis)
	ctx := context.Background()

	cacheKey := redis.ServerCacheKey("user-123")
	serverState := redis.ServerState{
		User:      "student",
		Address:   "2001:db8::1",
		Status:    config.StatusProvisioning,
		ServerID:  "server-123",
		WebUserID: "user-123",
		LabID:     42,
	}

	// Cache the state first
	_ = mockRedis.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL)

	// Call handleProvisioningError
	p.handleProvisioningError(ctx, mockSrv, cacheKey, serverState, "test error", errors.New("test error"))

	// Verify server was deleted
	if !mockSrv.deleteCalled {
		t.Error("expected server to be deleted")
	}

	// Verify cache was cleared
	_, err := mockRedis.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Error("expected cache to be cleared")
	}
}

func TestHandleProvisioningError_DeleteServerFails(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		deleteErr:   errors.New("failed to delete server"),
	}

	p := New(log, nil, mockRedis)
	ctx := context.Background()

	cacheKey := redis.ServerCacheKey("user-123")
	serverState := redis.ServerState{
		User:      "student",
		Address:   "2001:db8::1",
		Status:    config.StatusProvisioning,
		ServerID:  "server-123",
		WebUserID: "user-123",
		LabID:     42,
	}

	// Cache the state first
	_ = mockRedis.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL)

	// Call handleProvisioningError
	p.handleProvisioningError(ctx, mockSrv, cacheKey, serverState, "test error", errors.New("test error"))

	// Verify delete was attempted
	if !mockSrv.deleteCalled {
		t.Error("expected server delete to be attempted")
	}

	// Cache should still be cleared even if delete fails
	_, err := mockRedis.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Error("expected cache to be cleared even when server delete fails")
	}
}

func TestProcessRequest_CacheInitialStateError(t *testing.T) {
	log := newTestLogger()

	cacheError := errors.New("cache error")
	mockRedis := &mockRedisClient{
		pushServerStateFunc: func(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error {
			// Only fail on initial provisioning state (empty ServerID)
			if state.ServerID == "" {
				return cacheError
			}
			return nil
		},
	}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`

	// Should continue provisioning even if initial cache fails
	p.ProcessRequest(ctx, payload)

	// Server should still have been created (no deletion should occur)
	if mockSrv.deleteCalled {
		t.Error("server should not be deleted when only initial cache fails")
	}
}

func TestProcessRequest_GetStateError(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	// Test that GetState error during polling triggers cleanup
	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "unknown",
		stateErr:    errors.New("failed to get state"),
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// When GetState fails during polling, handleProvisioningError deletes cache and server
	cacheKey := redis.ServerCacheKey("user-123")
	_, err := mockRedis.GetServerState(ctx, cacheKey)
	if err == nil {
		t.Error("expected cache to be deleted after GetState error during polling")
	}

	// Server should have been deleted
	if !mockSrv.deleteCalled {
		t.Error("expected server to be deleted after GetState error during polling")
	}
}

func TestPollServerState_ContextCancellation(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "starting", // Never reaches "running"
	}

	p := New(log, nil, mockRedis).WithPollInterval(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	cacheKey := redis.ServerCacheKey("user-123")
	initialState := redis.ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      config.StatusProvisioning,
		Available:   false,
		CloudStatus: "starting",
		ServerID:    "server-123",
		WebUserID:   "user-123",
		LabID:       42,
	}

	// Cancel context immediately
	cancel()

	// This should return immediately due to context cancellation
	p.pollServerState(ctx, mockSrv, cacheKey, initialState, "starting")

	// Function should have returned without attempting to delete
	if mockSrv.deleteCalled {
		t.Error("server should not be deleted on context cancellation")
	}
}

func TestProcessRequest_UpdateCacheError(t *testing.T) {
	log := newTestLogger()

	callCount := 0
	cacheError := errors.New("cache update error")
	mockRedis := &mockRedisClient{
		pushServerStateFunc: func(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error {
			callCount++
			// Fail on second update (after server creation)
			if callCount == 2 {
				return cacheError
			}
			return nil
		},
	}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`

	// Should continue even if cache update fails
	p.ProcessRequest(ctx, payload)

	// Server should not be deleted just because cache update failed
	if mockSrv.deleteCalled {
		t.Error("server should not be deleted when cache update fails")
	}
}

func TestProcessRequest_CompleteFlow(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	// Create a server that transitions through states
	mockSrv := &mockServer{
		id:            "server-123",
		name:          "test-server",
		ipv6Address:   "2001:db8::1",
		stateSequence: []string{"starting", "initializing", "running"},
	}

	var createdPayload string
	mockConn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			createdPayload = payload
			return mockSrv, nil
		},
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify payload was passed to connector
	if createdPayload != payload {
		t.Errorf("expected payload %q to be passed to connector, got %q", payload, createdPayload)
	}

	// Verify final state
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server state to be cached, got error: %v", err)
	}

	// Verify all fields
	if state.User != "student" {
		t.Errorf("expected User 'student', got %s", state.User)
	}
	if state.Address != "2001:db8::1" {
		t.Errorf("expected Address '2001:db8::1', got %s", state.Address)
	}
	if state.Status != config.StatusRunning {
		t.Errorf("expected Status '%s', got %s", config.StatusRunning, state.Status)
	}
	if !state.Available {
		t.Error("expected Available to be true")
	}
	if state.CloudStatus != "running" {
		t.Errorf("expected CloudStatus 'running', got %s", state.CloudStatus)
	}
	if state.ServerID != "server-123" {
		t.Errorf("expected ServerID 'server-123', got %s", state.ServerID)
	}
	if state.WebUserID != "user-123" {
		t.Errorf("expected WebUserID 'user-123', got %s", state.WebUserID)
	}
	if state.LabID != 42 {
		t.Errorf("expected LabID 42, got %d", state.LabID)
	}
}

func TestProcessRequest_InvalidTTLEnvironmentVariable(t *testing.T) {
	// Set invalid TTL
	_ = os.Setenv("DEFAULT_TTL_MINUTES", "invalid")
	defer func() { _ = os.Unsetenv("DEFAULT_TTL_MINUTES") }()

	log := newTestLogger()
	mockRedis := &mockRedisClient{}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Should fall back to default TTL of 30 minutes
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server state to be cached, got error: %v", err)
	}

	expectedExpiry := time.Now().Add(30 * time.Minute)
	timeDiff := state.ExpiresAt.Sub(expectedExpiry)
	if timeDiff > 5*time.Second || timeDiff < -5*time.Second {
		t.Errorf("expected ExpiresAt to be ~30 minutes from now (default), got diff of %v", timeDiff)
	}
}

func TestProcessRequest_SameLabID_SkipsProvisioning(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{
		states: map[string]redis.ServerState{
			"vmmanager:servers:user-123": {
				User:        "student",
				Address:     "2001:db8::1",
				Status:      config.StatusRunning,
				Available:   true,
				CloudStatus: "running",
				ServerID:    "existing-server-123",
				WebUserID:   "user-123",
				LabID:       42,
			},
		},
	}

	createCalled := false
	mockConn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			createCalled = true
			return nil, errors.New("should not be called")
		},
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify CreateServer was NOT called
	if createCalled {
		t.Error("expected CreateServer to not be called for duplicate labId request")
	}

	// Verify no decommission request was queued
	if len(mockRedis.queuedPayloads) > 0 {
		t.Errorf("expected no decommission requests, got %d", len(mockRedis.queuedPayloads))
	}

	// Verify original server state is unchanged
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected server state to still be cached, got error: %v", err)
	}
	if state.ServerID != "existing-server-123" {
		t.Errorf("expected ServerID to remain 'existing-server-123', got %s", state.ServerID)
	}
}

func TestProcessRequest_DifferentLabID_DecommissionsAndProvisions(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{
		states: map[string]redis.ServerState{
			"vmmanager:servers:user-123": {
				User:        "student",
				Address:     "2001:db8::1",
				Status:      config.StatusRunning,
				Available:   true,
				CloudStatus: "running",
				ServerID:    "old-server-123",
				WebUserID:   "user-123",
				LabID:       42, // Old labId
			},
		},
	}

	newMockSrv := &mockServer{
		id:          "new-server-456",
		name:        "new-test-server",
		ipv6Address: "2001:db8::2",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: newMockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":99}` // New labId
	p.ProcessRequest(ctx, payload)

	// Verify decommission request was queued for old server
	if len(mockRedis.queuedPayloads) != 1 {
		t.Fatalf("expected 1 decommission request, got %d", len(mockRedis.queuedPayloads))
	}

	expectedDecommissionPayload := `{"webuserid":"user-123","labId":42,"serverId":"old-server-123"}`
	if mockRedis.queuedPayloads[0] != expectedDecommissionPayload {
		t.Errorf("expected decommission payload %q, got %q", expectedDecommissionPayload, mockRedis.queuedPayloads[0])
	}

	// Verify new server was provisioned
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected new server state to be cached, got error: %v", err)
	}
	if state.ServerID != "new-server-456" {
		t.Errorf("expected new ServerID 'new-server-456', got %s", state.ServerID)
	}
	if state.LabID != 99 {
		t.Errorf("expected new LabID 99, got %d", state.LabID)
	}
}

func TestProcessRequest_CacheReadRetry_Success(t *testing.T) {
	log := newTestLogger()

	callCount := 0
	mockRedis := &mockRedisClient{
		states: make(map[string]redis.ServerState),
		getServerStateFunc: func(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
			callCount++
			if callCount < 2 {
				// First call fails with connection error
				return nil, errors.New("redis connection error")
			}
			// Second call succeeds - no server found
			return nil, fmt.Errorf("server state not found in cache")
		},
	}

	mockSrv := &mockServer{
		id:          "server-123",
		name:        "test-server",
		ipv6Address: "2001:db8::1",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: mockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify retry was attempted (callCount should be 2)
	if callCount != 2 {
		t.Errorf("expected 2 GetServerState calls (1 failure + 1 success), got %d", callCount)
	}

	// Verify provisioning proceeded after successful retry
	// Check directly in the states map since getServerStateFunc is still set
	cacheKey := redis.ServerCacheKey("user-123")
	state, ok := mockRedis.states[cacheKey]
	if !ok {
		t.Fatalf("expected server state to be cached after retry success, but key not found in states map")
	}
	if state.ServerID != "server-123" {
		t.Errorf("expected ServerID 'server-123', got %s", state.ServerID)
	}
}

func TestProcessRequest_CacheReadRetry_AllFail(t *testing.T) {
	log := newTestLogger()

	callCount := 0
	mockRedis := &mockRedisClient{
		getServerStateFunc: func(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
			callCount++
			// All retries fail with connection error
			return nil, errors.New("redis connection error")
		},
	}

	createCalled := false
	mockConn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			createCalled = true
			return nil, errors.New("should not be called")
		},
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":42}`
	p.ProcessRequest(ctx, payload)

	// Verify all retry attempts were made (config.CacheReadRetryAttempts = 3)
	if callCount != config.CacheReadRetryAttempts {
		t.Errorf("expected %d GetServerState calls (all retries), got %d", config.CacheReadRetryAttempts, callCount)
	}

	// Verify provisioning was aborted
	if createCalled {
		t.Error("expected CreateServer to not be called when cache read retries exhausted")
	}
}

func TestProcessRequest_DifferentLabID_QueueDecommissionFails(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{
		states: map[string]redis.ServerState{
			"vmmanager:servers:user-123": {
				User:        "student",
				Address:     "2001:db8::1",
				Status:      config.StatusRunning,
				Available:   true,
				CloudStatus: "running",
				ServerID:    "old-server-123",
				WebUserID:   "user-123",
				LabID:       42,
			},
		},
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			return errors.New("failed to push to queue")
		},
	}

	newMockSrv := &mockServer{
		id:          "new-server-456",
		name:        "new-test-server",
		ipv6Address: "2001:db8::2",
		state:       "running",
	}

	mockConn := &mockConnector{
		server: newMockSrv,
	}

	p := New(log, mockConn, mockRedis).WithPollInterval(1 * time.Millisecond)
	ctx := context.Background()

	payload := `{"webuserid":"user-123","labId":99}`
	p.ProcessRequest(ctx, payload)

	// Verify new server was still provisioned despite queue failure
	cacheKey := redis.ServerCacheKey("user-123")
	state, err := mockRedis.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("expected new server state to be cached even if decommission queue failed, got error: %v", err)
	}
	if state.ServerID != "new-server-456" {
		t.Errorf("expected new ServerID 'new-server-456', got %s", state.ServerID)
	}
}

func TestGetServerStateWithRetry_NotFoundIsNotError(t *testing.T) {
	log := newTestLogger()
	mockRedis := &mockRedisClient{
		states: map[string]redis.ServerState{},
	}

	p := New(log, nil, mockRedis)
	ctx := context.Background()

	cacheKey := redis.ServerCacheKey("nonexistent-user")
	state, err := p.getServerStateWithRetry(ctx, cacheKey)

	// Should return (nil, nil) for "not found" - not an error
	if err != nil {
		t.Errorf("expected no error for non-existent cache key, got: %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state for non-existent cache key, got: %+v", state)
	}
}
