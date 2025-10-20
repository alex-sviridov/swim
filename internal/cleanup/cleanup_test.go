package cleanup

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

// Mock Connector
type mockConnector struct {
	mu                sync.Mutex
	deletedServers    map[string]bool
	createServerFunc  func(payload string) (connector.Server, error)
	getServerByIDFunc func(id string) (connector.Server, error)
	listServersFunc   func() ([]connector.Server, error)
}

func newMockConnector() *mockConnector {
	return &mockConnector{
		deletedServers: make(map[string]bool),
	}
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

func (m *mockConnector) markDeleted(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedServers[id] = true
}

func (m *mockConnector) wasDeleted(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deletedServers[id]
}

// Mock Server
type mockServer struct {
	id         string
	name       string
	ipv6       string
	state      string
	connector  *mockConnector
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
	if m.connector != nil {
		m.connector.markDeleted(m.id)
	}
	return nil
}

func (m *mockServer) String() string {
	return m.name + " [" + m.ipv6 + "]"
}

// Mock Redis Client
type mockRedisClient struct {
	mu               sync.Mutex
	serverStates     map[string]redis.ServerState
	pushStateFunc    func(ctx context.Context, key string, state redis.ServerState, ttl time.Duration) error
	getStateFunc     func(ctx context.Context, key string) (*redis.ServerState, error)
	popPayloadFunc   func(ctx context.Context, key string, timeout time.Duration) (string, error)
	getExpiredFunc   func(ctx context.Context, prefix string) ([]redis.ServerState, error)
}

func newMockRedisClient() *mockRedisClient {
	return &mockRedisClient{
		serverStates: make(map[string]redis.ServerState),
	}
}

func (m *mockRedisClient) PushServerState(ctx context.Context, key string, state redis.ServerState, ttl time.Duration) error {
	if m.pushStateFunc != nil {
		return m.pushStateFunc(ctx, key, state, ttl)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serverStates[key] = state
	return nil
}

func (m *mockRedisClient) GetServerState(ctx context.Context, key string) (*redis.ServerState, error) {
	if m.getStateFunc != nil {
		return m.getStateFunc(ctx, key)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()

	var expired []redis.ServerState
	now := time.Now()
	for _, state := range m.serverStates {
		if state.DeletionAt.Before(now) {
			expired = append(expired, state)
		}
	}
	return expired, nil
}

func (m *mockRedisClient) Close() error {
	return nil
}

func (m *mockRedisClient) DeleteServerState(ctx context.Context, cacheKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.serverStates, cacheKey)
	return nil
}

func TestWorkerNew(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	worker := New(log, conn, redisClient)

	if worker == nil {
		t.Fatal("Expected non-nil worker")
	}
	if worker.log != log {
		t.Error("Logger not set correctly")
	}
	if worker.conn != conn {
		t.Error("Connector not set correctly")
	}
	// Redis client is set correctly if New() succeeded without panic
}

func TestCleanupExpiredServers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	now := time.Now()

	// Setup expired servers
	expiredServers := []redis.ServerState{
		{
			ID:            "expired-1",
			Name:          "Expired Server 1",
			IPv6:          "2001:db8::1",
			State:         config.StateBooted,
			ProvisionedAt: now.Add(-2 * time.Hour),
			DeletionAt:    now.Add(-1 * time.Hour),
		},
		{
			ID:            "expired-2",
			Name:          "Expired Server 2",
			IPv6:          "2001:db8::2",
			State:         config.StateBooted,
			ProvisionedAt: now.Add(-3 * time.Hour),
			DeletionAt:    now.Add(-30 * time.Minute),
		},
	}

	for _, state := range expiredServers {
		key := redis.ServerCacheKey(state.ID)
		redisClient.serverStates[key] = state
	}

	// Setup connector to return mock servers
	conn.getServerByIDFunc = func(id string) (connector.Server, error) {
		return &mockServer{
			id:        id,
			name:      "test-server",
			ipv6:      "2001:db8::1",
			state:     config.StateBooted,
			connector: conn,
		}, nil
	}

	worker := New(log, conn, redisClient)
	ctx := context.Background()

	worker.cleanupExpiredServers(ctx)

	// Give goroutines time to complete
	time.Sleep(100 * time.Millisecond)

	// Verify both servers were deleted
	if !conn.wasDeleted("expired-1") {
		t.Error("expired-1 should have been deleted")
	}
	if !conn.wasDeleted("expired-2") {
		t.Error("expired-2 should have been deleted")
	}
}

func TestCleanupExpiredServersNone(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	worker := New(log, conn, redisClient)
	ctx := context.Background()

	worker.cleanupExpiredServers(ctx)

	// Should complete without errors when no expired servers
}

func TestCleanupExpiredServersGetServerFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	now := time.Now()

	// Setup expired server
	expiredServer := redis.ServerState{
		ID:            "expired-1",
		Name:          "Expired Server 1",
		IPv6:          "2001:db8::1",
		State:         config.StateBooted,
		ProvisionedAt: now.Add(-2 * time.Hour),
		DeletionAt:    now.Add(-1 * time.Hour),
	}

	key := redis.ServerCacheKey(expiredServer.ID)
	redisClient.serverStates[key] = expiredServer

	// GetServerByID fails
	conn.getServerByIDFunc = func(id string) (connector.Server, error) {
		return nil, errors.New("server not found")
	}

	worker := New(log, conn, redisClient)
	ctx := context.Background()

	worker.cleanupExpiredServers(ctx)

	// Give goroutines time to complete
	time.Sleep(100 * time.Millisecond)

	// Should not crash, just log error
}

func TestCleanupExpiredServersDeleteFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	now := time.Now()

	// Setup expired server
	expiredServer := redis.ServerState{
		ID:            "expired-1",
		Name:          "Expired Server 1",
		IPv6:          "2001:db8::1",
		State:         config.StateBooted,
		ProvisionedAt: now.Add(-2 * time.Hour),
		DeletionAt:    now.Add(-1 * time.Hour),
	}

	key := redis.ServerCacheKey(expiredServer.ID)
	redisClient.serverStates[key] = expiredServer

	// GetServerByID returns server that fails to delete
	conn.getServerByIDFunc = func(id string) (connector.Server, error) {
		return &mockServer{
			id:   id,
			name: "test-server",
			ipv6: "2001:db8::1",
			deleteFunc: func() error {
				return errors.New("delete failed")
			},
		}, nil
	}

	worker := New(log, conn, redisClient)
	ctx := context.Background()

	worker.cleanupExpiredServers(ctx)

	// Give goroutines time to complete
	time.Sleep(100 * time.Millisecond)

	// Should not crash, just log error
}

func TestCleanupExpiredServersGetExpiredFails(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	// GetExpiredServers fails
	redisClient.getExpiredFunc = func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
		return nil, errors.New("redis scan failed")
	}

	worker := New(log, conn, redisClient)
	ctx := context.Background()

	worker.cleanupExpiredServers(ctx)

	// Should return early without crashing
}

func TestWorkerRunAndStop(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	worker := New(log, conn, redisClient)

	ctx, cancel := context.WithCancel(context.Background())

	// Start worker in goroutine
	done := make(chan bool)
	go func() {
		worker.Run(ctx)
		done <- true
	}()

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Cancel context
	cancel()

	// Wait for worker to stop
	select {
	case <-done:
		// Worker stopped successfully
	case <-time.After(2 * time.Second):
		t.Error("Worker did not stop within timeout")
	}
}

func TestDeleteExpiredServer(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	now := time.Now()
	state := redis.ServerState{
		ID:            "test-server",
		Name:          "Test Server",
		IPv6:          "2001:db8::1",
		State:         config.StateBooted,
		ProvisionedAt: now.Add(-2 * time.Hour),
		DeletionAt:    now.Add(-1 * time.Hour),
	}

	conn.getServerByIDFunc = func(id string) (connector.Server, error) {
		return &mockServer{
			id:        id,
			name:      state.Name,
			ipv6:      state.IPv6,
			state:     state.State,
			connector: conn,
		}, nil
	}

	worker := New(log, conn, redisClient)
	worker.deleteExpiredServer(state)

	// Give goroutine time to complete
	time.Sleep(50 * time.Millisecond)

	if !conn.wasDeleted(state.ID) {
		t.Error("Server should have been deleted")
	}
}

func TestCleanupWithContextCancellation(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	now := time.Now()

	// Setup many expired servers
	for i := 0; i < 100; i++ {
		state := redis.ServerState{
			ID:            string(rune('a' + i)),
			Name:          "Test Server",
			IPv6:          "2001:db8::1",
			State:         config.StateBooted,
			ProvisionedAt: now.Add(-2 * time.Hour),
			DeletionAt:    now.Add(-1 * time.Hour),
		}
		key := redis.ServerCacheKey(state.ID)
		redisClient.serverStates[key] = state
	}

	worker := New(log, conn, redisClient)
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel context immediately
	cancel()

	worker.cleanupExpiredServers(ctx)

	// Should exit early without processing all servers
}

func TestCleanupMultipleConcurrentDeletes(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	conn := newMockConnector()
	redisClient := newMockRedisClient()

	now := time.Now()

	// Setup multiple expired servers
	numServers := 10
	for i := 0; i < numServers; i++ {
		state := redis.ServerState{
			ID:            string(rune('a' + i)),
			Name:          "Test Server",
			IPv6:          "2001:db8::1",
			State:         config.StateBooted,
			ProvisionedAt: now.Add(-2 * time.Hour),
			DeletionAt:    now.Add(-1 * time.Hour),
		}
		key := redis.ServerCacheKey(state.ID)
		redisClient.serverStates[key] = state
	}

	conn.getServerByIDFunc = func(id string) (connector.Server, error) {
		return &mockServer{
			id:        id,
			name:      "test-server",
			ipv6:      "2001:db8::1",
			state:     config.StateBooted,
			connector: conn,
		}, nil
	}

	worker := New(log, conn, redisClient)
	ctx := context.Background()

	worker.cleanupExpiredServers(ctx)

	// Give goroutines time to complete
	time.Sleep(200 * time.Millisecond)

	// Verify all servers were deleted
	for i := 0; i < numServers; i++ {
		id := string(rune('a' + i))
		if !conn.wasDeleted(id) {
			t.Errorf("Server %s should have been deleted", id)
		}
	}
}
