package decommissioner

import (
	"context"
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
	createServerFunc  func(payload string) (connector.Server, error)
	getServerByIDFunc func(id string) (connector.Server, error)
	listServersFunc   func() ([]connector.Server, error)
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
	id         string
	name       string
	ipv6       string
	state      string
	deleteFunc func() error
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
	getByFilterFunc   func(ctx context.Context, prefix string, username string, labID *int) ([]redis.ServerState, error)
	deleteStateFunc   func(ctx context.Context, cacheKey string) error
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

func (m *mockRedisClient) GetServersByFilter(ctx context.Context, prefix string, username string, labID *int) ([]redis.ServerState, error) {
	if m.getByFilterFunc != nil {
		return m.getByFilterFunc(ctx, prefix, username, labID)
	}
	return nil, errors.New("not implemented")
}

func (m *mockRedisClient) DeleteServerState(ctx context.Context, cacheKey string) error {
	if m.deleteStateFunc != nil {
		return m.deleteStateFunc(ctx, cacheKey)
	}
	if m.serverStates != nil {
		delete(m.serverStates, cacheKey)
	}
	return nil
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

func TestDecommissionerNew(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	conn := &mockConnector{}
	redisClient := &mockRedisClient{}

	decomm := New(log, conn, redisClient)

	if decomm == nil {
		t.Fatal("Expected non-nil decommissioner")
	}
	if decomm.log != log {
		t.Error("Logger not set correctly")
	}
	if decomm.conn != conn {
		t.Error("Connector not set correctly")
	}
}

func TestProcessRequestUsernameOnly(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	deletedServers := make(map[string]bool)

	// Mock two servers for the same user
	servers := []redis.ServerState{
		{
			ID:          "server-1",
			Name:        "test-server-1",
			IPv6:        "2001:db8::1",
			State:       "running",
			WebUsername: "testuser",
			LabID:       123,
		},
		{
			ID:          "server-2",
			Name:        "test-server-2",
			IPv6:        "2001:db8::2",
			State:       "running",
			WebUsername: "testuser",
			LabID:       456,
		},
	}

	conn := &mockConnector{
		getServerByIDFunc: func(id string) (connector.Server, error) {
			return &mockServer{
				id:   id,
				name: "test-server",
				ipv6: "2001:db8::1",
				deleteFunc: func() error {
					deletedServers[id] = true
					return nil
				},
			}, nil
		},
	}

	redisClient := &mockRedisClient{
		getByFilterFunc: func(ctx context.Context, prefix string, username string, labID *int) ([]redis.ServerState, error) {
			if username == "testuser" && labID == nil {
				return servers, nil
			}
			return []redis.ServerState{}, nil
		},
		serverStates: make(map[string]redis.ServerState),
	}

	decomm := New(log, conn, redisClient)

	payload := `{"WebUsername":"testuser"}`

	ctx := context.Background()
	decomm.ProcessRequest(ctx, payload)

	// Verify both servers were deleted
	if !deletedServers["server-1"] {
		t.Error("server-1 should have been deleted")
	}
	if !deletedServers["server-2"] {
		t.Error("server-2 should have been deleted")
	}
}

func TestProcessRequestUsernameAndLabID(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	deletedServers := make(map[string]bool)

	// Mock one server matching username and labID
	servers := []redis.ServerState{
		{
			ID:          "server-1",
			Name:        "test-server-1",
			IPv6:        "2001:db8::1",
			State:       "running",
			WebUsername: "testuser",
			LabID:       123,
		},
	}

	conn := &mockConnector{
		getServerByIDFunc: func(id string) (connector.Server, error) {
			return &mockServer{
				id:   id,
				name: "test-server",
				ipv6: "2001:db8::1",
				deleteFunc: func() error {
					deletedServers[id] = true
					return nil
				},
			}, nil
		},
	}

	redisClient := &mockRedisClient{
		getByFilterFunc: func(ctx context.Context, prefix string, username string, labID *int) ([]redis.ServerState, error) {
			if username == "testuser" && labID != nil && *labID == 123 {
				return servers, nil
			}
			return []redis.ServerState{}, nil
		},
		serverStates: make(map[string]redis.ServerState),
	}

	decomm := New(log, conn, redisClient)

	payload := `{"WebUsername":"testuser","LabID":123}`

	ctx := context.Background()
	decomm.ProcessRequest(ctx, payload)

	// Verify only the matching server was deleted
	if !deletedServers["server-1"] {
		t.Error("server-1 should have been deleted")
	}
	if len(deletedServers) != 1 {
		t.Errorf("Expected 1 deleted server, got %d", len(deletedServers))
	}
}

func TestProcessRequestInvalidPayload(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := &mockConnector{}
	redisClient := &mockRedisClient{}

	decomm := New(log, conn, redisClient)

	// Invalid JSON payload
	payload := `{invalid json}`

	ctx := context.Background()
	decomm.ProcessRequest(ctx, payload)

	// Should return early without crashing
}

func TestProcessRequestMissingUsername(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := &mockConnector{}
	redisClient := &mockRedisClient{}

	decomm := New(log, conn, redisClient)

	// Payload without WebUsername
	payload := `{"LabID":123}`

	ctx := context.Background()
	decomm.ProcessRequest(ctx, payload)

	// Should return early without crashing
}

func TestProcessRequestNoServersFound(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	conn := &mockConnector{}
	redisClient := &mockRedisClient{
		getByFilterFunc: func(ctx context.Context, prefix string, username string, labID *int) ([]redis.ServerState, error) {
			return []redis.ServerState{}, nil
		},
	}

	decomm := New(log, conn, redisClient)

	payload := `{"WebUsername":"testuser"}`

	ctx := context.Background()
	decomm.ProcessRequest(ctx, payload)

	// Should complete without error
}

func TestProcessRequestGetServersFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	conn := &mockConnector{}
	redisClient := &mockRedisClient{
		getByFilterFunc: func(ctx context.Context, prefix string, username string, labID *int) ([]redis.ServerState, error) {
			return nil, errors.New("redis error")
		},
	}

	decomm := New(log, conn, redisClient)

	payload := `{"WebUsername":"testuser"}`

	ctx := context.Background()
	decomm.ProcessRequest(ctx, payload)

	// Should return early without crashing
}

func TestDeleteServerSuccess(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	deleted := false
	conn := &mockConnector{
		getServerByIDFunc: func(id string) (connector.Server, error) {
			return &mockServer{
				id:   id,
				name: "test-server",
				ipv6: "2001:db8::1",
				deleteFunc: func() error {
					deleted = true
					return nil
				},
			}, nil
		},
	}

	cacheDeleted := false
	redisClient := &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
		deleteStateFunc: func(ctx context.Context, cacheKey string) error {
			cacheDeleted = true
			return nil
		},
	}

	decomm := New(log, conn, redisClient)

	serverState := redis.ServerState{
		ID:          "server-1",
		Name:        "test-server",
		IPv6:        "2001:db8::1",
		State:       "running",
		WebUsername: "testuser",
		LabID:       123,
	}

	ctx := context.Background()
	decomm.deleteServer(ctx, serverState)

	if !deleted {
		t.Error("Server should have been deleted")
	}
	if !cacheDeleted {
		t.Error("Cache should have been deleted")
	}
}

func TestDeleteServerNotFound(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	conn := &mockConnector{
		getServerByIDFunc: func(id string) (connector.Server, error) {
			return nil, errors.New("server not found")
		},
	}

	cacheDeleted := false
	redisClient := &mockRedisClient{
		deleteStateFunc: func(ctx context.Context, cacheKey string) error {
			cacheDeleted = true
			return nil
		},
	}

	decomm := New(log, conn, redisClient)

	serverState := redis.ServerState{
		ID:          "server-1",
		Name:        "test-server",
		IPv6:        "2001:db8::1",
		State:       "running",
		WebUsername: "testuser",
		LabID:       123,
	}

	ctx := context.Background()
	decomm.deleteServer(ctx, serverState)

	// Cache should still be deleted even if server not found
	if !cacheDeleted {
		t.Error("Cache should have been deleted")
	}
}

func TestDeleteServerDeleteFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	conn := &mockConnector{
		getServerByIDFunc: func(id string) (connector.Server, error) {
			return &mockServer{
				id:   id,
				name: "test-server",
				ipv6: "2001:db8::1",
				deleteFunc: func() error {
					return errors.New("delete failed")
				},
			}, nil
		},
	}

	redisClient := &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
	}

	decomm := New(log, conn, redisClient)

	serverState := redis.ServerState{
		ID:          "server-1",
		Name:        "test-server",
		IPv6:        "2001:db8::1",
		State:       "running",
		WebUsername: "testuser",
		LabID:       123,
	}

	ctx := context.Background()
	decomm.deleteServer(ctx, serverState)

	// Verify error state was cached
	cacheKey := redis.ServerCacheKey(serverState.ID)
	state, err := redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		t.Fatalf("Failed to get cached state: %v", err)
	}

	if state.State != config.StateDeletedError {
		t.Errorf("Expected state %v, got %v", config.StateDeletedError, state.State)
	}
}
