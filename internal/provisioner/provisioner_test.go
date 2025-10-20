package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

// Mock Connector
type mockConnector struct {
	createServerFunc   func(payload string) (connector.Server, error)
	getServerByIDFunc  func(id string) (connector.Server, error)
	listServersFunc    func() ([]connector.Server, error)
}

func (m *mockConnector) CreateServer(payload string) (connector.Server, error) {
	if m.createServerFunc != nil {
		return m.createServerFunc(payload)
	}
	return nil, errors.New("not implemented")
}

func (m *mockConnector) GetServerByID(id string) (connector.Server, error) {
	if m.getServerByIDFunc != nil {
		return m.getServerByIDFunc(id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockConnector) ListServers() ([]connector.Server, error) {
	if m.listServersFunc != nil {
		return m.listServersFunc()
	}
	return nil, errors.New("not implemented")
}

// Mock Server
type mockServer struct {
	id           string
	name         string
	ipv6         string
	state        string
	stateChanges []string // Simulate state transitions
	stateIndex   int
	deleteFunc   func() error
}

func (m *mockServer) GetID() string {
	return m.id
}

func (m *mockServer) GetName() string {
	return m.name
}

func (m *mockServer) GetIPv6Address() string {
	return m.ipv6
}

func (m *mockServer) GetState() (string, error) {
	if len(m.stateChanges) > 0 && m.stateIndex < len(m.stateChanges) {
		state := m.stateChanges[m.stateIndex]
		m.stateIndex++
		return state, nil
	}
	return m.state, nil
}

func (m *mockServer) Delete() error {
	if m.deleteFunc != nil {
		return m.deleteFunc()
	}
	return nil
}

func (m *mockServer) String() string {
	return m.name + " [" + m.ipv6 + "]"
}

// Mock Redis Client
type mockRedisClient struct {
	serverStates      map[string]redis.ServerState
	pushStateFunc     func(ctx context.Context, key string, state redis.ServerState, ttl time.Duration) error
	getStateFunc      func(ctx context.Context, key string) (*redis.ServerState, error)
	popPayloadFunc    func(ctx context.Context, key string, timeout time.Duration) (string, error)
	getExpiredFunc    func(ctx context.Context, prefix string) ([]redis.ServerState, error)
}

func (m *mockRedisClient) PushServerState(ctx context.Context, key string, state redis.ServerState, ttl time.Duration) error {
	if m.pushStateFunc != nil {
		return m.pushStateFunc(ctx, key, state, ttl)
	}
	if m.serverStates == nil {
		m.serverStates = make(map[string]redis.ServerState)
	}
	m.serverStates[key] = state
	return nil
}

func (m *mockRedisClient) GetServerState(ctx context.Context, key string) (*redis.ServerState, error) {
	if m.getStateFunc != nil {
		return m.getStateFunc(ctx, key)
	}
	state, ok := m.serverStates[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return &state, nil
}

func (m *mockRedisClient) PopPayload(ctx context.Context, key string, timeout time.Duration) (string, error) {
	if m.popPayloadFunc != nil {
		return m.popPayloadFunc(ctx, key, timeout)
	}
	return "", errors.New("not implemented")
}

func (m *mockRedisClient) GetExpiredServers(ctx context.Context, prefix string) ([]redis.ServerState, error) {
	if m.getExpiredFunc != nil {
		return m.getExpiredFunc(ctx, prefix)
	}
	return nil, errors.New("not implemented")
}

func (m *mockRedisClient) Close() error {
	return nil
}

func (m *mockRedisClient) DeleteServerState(ctx context.Context, cacheKey string) error {
	if m.serverStates != nil {
		delete(m.serverStates, cacheKey)
	}
	return nil
}

func TestProvisionerNew(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	conn := &mockConnector{}
	redisClient := &mockRedisClient{}

	prov := New(log, conn, redisClient)

	if prov == nil {
		t.Fatal("Expected non-nil provisioner")
	}
	if prov.log != log {
		t.Error("Logger not set correctly")
	}
	if prov.conn != conn {
		t.Error("Connector not set correctly")
	}
	// Redis client is set correctly if New() succeeded without panic
}

func TestProcessRequestSuccess(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	server := &mockServer{
		id:   "server-123",
		name: "test-server",
		ipv6: "2001:db8::1",
		stateChanges: []string{"starting", "starting", config.StateBooted},
	}

	conn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			return server, nil
		},
	}

	redisClient := &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
	}

	prov := New(log, conn, redisClient)

	payload := `{
		"ServerType": "DEV1-S",
		"SecurityGroupName": "default",
		"ImageID": "ubuntu-22.04",
		"WebUsername": "testuser",
		"WebLabID": 123,
		"TTLMinutes": 60
	}`

	ctx := context.Background()
	prov.ProcessRequest(ctx, payload)

	// Verify server state was cached
	cacheKey := redis.ServerCacheKey(server.GetID())
	state, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to get cached state: %v", err)
	}

	if state.ID != server.GetID() {
		t.Errorf("Cached ID = %v, want %v", state.ID, server.GetID())
	}
	if state.Name != server.GetName() {
		t.Errorf("Cached Name = %v, want %v", state.Name, server.GetName())
	}
	if state.IPv6 != server.GetIPv6Address() {
		t.Errorf("Cached IPv6 = %v, want %v", state.IPv6, server.GetIPv6Address())
	}
}

func TestProcessRequestInvalidPayload(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := &mockConnector{}
	redisClient := &mockRedisClient{}

	prov := New(log, conn, redisClient)

	// Invalid JSON payload
	payload := `{invalid json}`

	ctx := context.Background()
	prov.ProcessRequest(ctx, payload)

	// Should return early without crashing
}

func TestProcessRequestCreateServerFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	conn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			return nil, errors.New("create server failed")
		},
	}

	redisClient := &mockRedisClient{}

	prov := New(log, conn, redisClient)

	payload := `{
		"ServerType": "DEV1-S",
		"SecurityGroupName": "default",
		"ImageID": "ubuntu-22.04",
		"WebUsername": "testuser",
		"WebLabID": 123,
		"TTLMinutes": 60
	}`

	ctx := context.Background()
	prov.ProcessRequest(ctx, payload)

	// Should return early without caching anything
	if len(redisClient.serverStates) > 0 {
		t.Error("Should not cache state when server creation fails")
	}
}

func TestProcessRequestCacheFailure(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	server := &mockServer{
		id:    "server-123",
		name:  "test-server",
		ipv6:  "2001:db8::1",
		state: config.StateBooted,
	}

	conn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			return server, nil
		},
	}

	redisClient := &mockRedisClient{
		pushStateFunc: func(ctx context.Context, key string, state redis.ServerState, ttl time.Duration) error {
			return errors.New("cache push failed")
		},
	}

	prov := New(log, conn, redisClient)

	payload := `{
		"ServerType": "DEV1-S",
		"SecurityGroupName": "default",
		"ImageID": "ubuntu-22.04",
		"WebUsername": "testuser",
		"WebLabID": 123,
		"TTLMinutes": 60
	}`

	ctx := context.Background()
	prov.ProcessRequest(ctx, payload)

	// Should continue despite cache failure
}

func TestProcessRequestGetStateFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create a mock server with getStateFunc that returns error
	server := &mockServer{
		id:   "server-123",
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	conn := &mockConnector{
		createServerFunc: func(payload string) (connector.Server, error) {
			// Return a server that will fail on GetState
			return &mockServerWithError{
				id:   "server-123",
				name: "test-server",
				ipv6: "2001:db8::1",
			}, nil
		},
	}

	redisClient := &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
	}

	prov := New(log, conn, redisClient)

	payload := `{
		"ServerType": "DEV1-S",
		"SecurityGroupName": "default",
		"ImageID": "ubuntu-22.04",
		"WebUsername": "testuser",
		"WebLabID": 123,
		"TTLMinutes": 60
	}`

	ctx := context.Background()
	prov.ProcessRequest(ctx, payload)

	// When GetState fails during polling, it triggers error handling
	// which deletes the server and sets state to "deleted-error"
	cacheKey := redis.ServerCacheKey(server.id)
	state, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to get cached state: %v", err)
	}

	if state.State != config.StateDeletedError {
		t.Errorf("Expected state %v, got %v", config.StateDeletedError, state.State)
	}
}

// mockServerWithError always returns an error on GetState
type mockServerWithError struct {
	id   string
	name string
	ipv6 string
}

func (m *mockServerWithError) GetID() string {
	return m.id
}

func (m *mockServerWithError) GetName() string {
	return m.name
}

func (m *mockServerWithError) GetIPv6Address() string {
	return m.ipv6
}

func (m *mockServerWithError) GetState() (string, error) {
	return "", errors.New("get state failed")
}

func (m *mockServerWithError) Delete() error {
	return nil
}

func (m *mockServerWithError) String() string {
	return m.name + " [" + m.ipv6 + "]"
}

func TestHandleProvisioningError(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	deleted := false
	server := &mockServer{
		id:   "server-123",
		name: "test-server",
		ipv6: "2001:db8::1",
		deleteFunc: func() error {
			deleted = true
			return nil
		},
	}

	conn := &mockConnector{}
	redisClient := &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
	}

	prov := New(log, conn, redisClient)

	ctx := context.Background()
	cacheKey := redis.ServerCacheKey(server.GetID())
	serverState := redis.ServerState{
		ID:            server.GetID(),
		Name:          server.GetName(),
		IPv6:          server.GetIPv6Address(),
		State:         "running",
		ProvisionedAt: time.Now(),
		DeletionAt:    time.Now().Add(1 * time.Hour),
	}

	prov.handleProvisioningError(ctx, server, cacheKey, serverState, "test error", errors.New("test"))

	// Verify server was deleted
	if !deleted {
		t.Error("Server should have been deleted")
	}

	// Verify state was updated to deleted-error
	state, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to get cached state: %v", err)
	}

	if state.State != config.StateDeletedError {
		t.Errorf("Expected state %v, got %v", config.StateDeletedError, state.State)
	}
}

func TestHandleProvisioningErrorDeleteFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	server := &mockServer{
		id:   "server-123",
		name: "test-server",
		ipv6: "2001:db8::1",
		deleteFunc: func() error {
			return errors.New("delete failed")
		},
	}

	conn := &mockConnector{}
	redisClient := &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
	}

	prov := New(log, conn, redisClient)

	ctx := context.Background()
	cacheKey := redis.ServerCacheKey(server.GetID())
	serverState := redis.ServerState{
		ID:            server.GetID(),
		Name:          server.GetName(),
		IPv6:          server.GetIPv6Address(),
		State:         "running",
		ProvisionedAt: time.Now(),
		DeletionAt:    time.Now().Add(1 * time.Hour),
	}

	prov.handleProvisioningError(ctx, server, cacheKey, serverState, "test error", errors.New("test"))

	// Even if delete fails, state should be updated
	state, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to get cached state: %v", err)
	}

	if state.State != config.StateDeletedError {
		t.Errorf("Expected state %v, got %v", config.StateDeletedError, state.State)
	}
}

func TestTTLParsing(t *testing.T) {
	tests := []struct {
		name        string
		ttlMinutes  int
		wantMinDiff time.Duration
		wantMaxDiff time.Duration
	}{
		{
			name:        "60 minutes",
			ttlMinutes:  60,
			wantMinDiff: 59 * time.Minute,
			wantMaxDiff: 61 * time.Minute,
		},
		{
			name:        "30 minutes",
			ttlMinutes:  30,
			wantMinDiff: 29 * time.Minute,
			wantMaxDiff: 31 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

			server := &mockServer{
				id:    "server-123",
				name:  "test-server",
				ipv6:  "2001:db8::1",
				state: config.StateBooted,
			}

			conn := &mockConnector{
				createServerFunc: func(payload string) (connector.Server, error) {
					return server, nil
				},
			}

			redisClient := &mockRedisClient{
				serverStates: make(map[string]redis.ServerState),
			}

			prov := New(log, conn, redisClient)

			payload := map[string]interface{}{
				"ServerType":        "DEV1-S",
				"SecurityGroupName": "default",
				"ImageID":           "ubuntu-22.04",
				"WebUsername":       "testuser",
				"WebLabID":          123,
				"TTLMinutes":        tt.ttlMinutes,
			}

			payloadJSON, _ := json.Marshal(payload)

			ctx := context.Background()
			now := time.Now()
			prov.ProcessRequest(ctx, string(payloadJSON))

			cacheKey := redis.ServerCacheKey(server.GetID())
			state, err := redisClient.GetServerState(ctx, cacheKey)
			if err != nil {
				t.Fatalf("Failed to get cached state: %v", err)
			}

			diff := state.DeletionAt.Sub(now)
			if diff < tt.wantMinDiff || diff > tt.wantMaxDiff {
				t.Errorf("DeletionAt diff = %v, want between %v and %v", diff, tt.wantMinDiff, tt.wantMaxDiff)
			}
		})
	}
}
