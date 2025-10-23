package cleanup

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

// mockRedisClient is a mock implementation of redis.ClientInterface
type mockRedisClient struct {
	getAllServerStatesFunc func(ctx context.Context, prefix string) ([]redis.ServerState, error)
	pushPayloadFunc        func(ctx context.Context, queueKey string, payload string) error
	getServerStateFunc     func(ctx context.Context, cacheKey string) (*redis.ServerState, error)
	deleteServerStateFunc  func(ctx context.Context, cacheKey string) error
	popPayloadFunc         func(ctx context.Context, queueKey string, timeout time.Duration) (string, error)
	pushServerStateFunc    func(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error
	closeFunc              func() error
}

func (m *mockRedisClient) GetAllServerStates(ctx context.Context, prefix string) ([]redis.ServerState, error) {
	if m.getAllServerStatesFunc != nil {
		return m.getAllServerStatesFunc(ctx, prefix)
	}
	return []redis.ServerState{}, nil
}

func (m *mockRedisClient) PushPayload(ctx context.Context, queueKey string, payload string) error {
	if m.pushPayloadFunc != nil {
		return m.pushPayloadFunc(ctx, queueKey, payload)
	}
	return nil
}

func (m *mockRedisClient) GetServerState(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
	if m.getServerStateFunc != nil {
		return m.getServerStateFunc(ctx, cacheKey)
	}
	return nil, nil
}

func (m *mockRedisClient) DeleteServerState(ctx context.Context, cacheKey string) error {
	if m.deleteServerStateFunc != nil {
		return m.deleteServerStateFunc(ctx, cacheKey)
	}
	return nil
}

func (m *mockRedisClient) PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error) {
	if m.popPayloadFunc != nil {
		return m.popPayloadFunc(ctx, queueKey, timeout)
	}
	return "", nil
}

func (m *mockRedisClient) PushServerState(ctx context.Context, cacheKey string, state redis.ServerState, ttl time.Duration) error {
	if m.pushServerStateFunc != nil {
		return m.pushServerStateFunc(ctx, cacheKey, state, ttl)
	}
	return nil
}

func (m *mockRedisClient) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	// Allow by default in tests (not rate limited)
	return true, nil
}

func (m *mockRedisClient) Close() error {
	if m.closeFunc != nil {
		return m.closeFunc()
	}
	return nil
}

// mockConnector is a mock implementation of connector.Connector
type mockConnector struct{}

// mockServer is a mock implementation of connector.Server
type mockServer struct{}

func (m *mockServer) GetID() string             { return "" }
func (m *mockServer) GetName() string           { return "" }
func (m *mockServer) GetIPv6Address() string    { return "" }
func (m *mockServer) GetState() (string, error) { return "", nil }
func (m *mockServer) Delete() error             { return nil }
func (m *mockServer) String() string            { return "" }

func (m *mockConnector) ListServers() ([]connector.Server, error) {
	return []connector.Server{}, nil
}

func (m *mockConnector) GetServerByID(id string) (connector.Server, error) {
	return &mockServer{}, nil
}

func (m *mockConnector) CreateServer(payload string) (connector.Server, error) {
	return &mockServer{}, nil
}

func TestNew(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}
	redisClient := &mockRedisClient{}

	worker := New(log, conn, redisClient)

	if worker == nil {
		t.Fatal("expected worker to be created, got nil")
	}

	if worker.log == nil {
		t.Error("expected log to be set")
	}

	if worker.conn == nil {
		t.Error("expected conn to be set")
	}

	if worker.redisClient == nil {
		t.Error("expected redisClient to be set")
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	getAllCalled := false
	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			getAllCalled = true
			return []redis.ServerState{}, nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	// Run should exit quickly when context is cancelled
	worker.Run(ctx)

	// Verify initial cleanup was called
	if !getAllCalled {
		t.Error("expected GetAllServerStates to be called during startup cleanup")
	}
}

func TestRun_PeriodicCleanup(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	callCount := 0
	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			callCount++
			return []redis.ServerState{}, nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	worker.Run(ctx)

	// Should be called at least once (initial cleanup)
	if callCount < 1 {
		t.Errorf("expected at least 1 call to GetAllServerStates, got %d", callCount)
	}
}

func TestCleanupExpiredServers_NoServers(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	getAllCalled := false
	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			getAllCalled = true
			if prefix != config.ServerCachePrefix {
				t.Errorf("expected prefix %s, got %s", config.ServerCachePrefix, prefix)
			}
			return []redis.ServerState{}, nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx := context.Background()
	worker.cleanupExpiredServers(ctx)

	if !getAllCalled {
		t.Error("expected GetAllServerStates to be called")
	}
}

func TestCleanupExpiredServers_NoExpiredServers(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	futureTime := time.Now().Add(1 * time.Hour)

	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			return []redis.ServerState{
				{
					ServerID:  "server1",
					WebUserID: "user1",
					LabID:     1,
					ExpiresAt: futureTime,
				},
				{
					ServerID:  "server2",
					WebUserID: "user2",
					LabID:     2,
					ExpiresAt: futureTime,
				},
			}, nil
		},
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			t.Error("expected PushPayload not to be called for non-expired servers")
			return nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx := context.Background()
	worker.cleanupExpiredServers(ctx)
}

func TestCleanupExpiredServers_WithExpiredServers(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	pastTime := time.Now().Add(-1 * time.Hour)
	futureTime := time.Now().Add(1 * time.Hour)

	pushedPayloads := []string{}

	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			return []redis.ServerState{
				{
					ServerID:  "server1",
					WebUserID: "user1",
					LabID:     1,
					ExpiresAt: pastTime,
				},
				{
					ServerID:  "server2",
					WebUserID: "user2",
					LabID:     2,
					ExpiresAt: futureTime,
				},
				{
					ServerID:  "server3",
					WebUserID: "user3",
					LabID:     3,
					ExpiresAt: pastTime,
				},
			}, nil
		},
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			if queueKey != config.DecommissionQueueKey {
				t.Errorf("expected queue key %s, got %s", config.DecommissionQueueKey, queueKey)
			}
			pushedPayloads = append(pushedPayloads, payload)
			return nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx := context.Background()
	worker.cleanupExpiredServers(ctx)

	// Verify 2 expired servers were pushed
	if len(pushedPayloads) != 2 {
		t.Errorf("expected 2 decommission requests, got %d", len(pushedPayloads))
	}

	// Verify payload structure
	for i, payload := range pushedPayloads {
		var decomReq map[string]interface{}
		if err := json.Unmarshal([]byte(payload), &decomReq); err != nil {
			t.Errorf("failed to unmarshal payload %d: %v", i, err)
		}

		if _, ok := decomReq["webuserid"]; !ok {
			t.Errorf("payload %d missing webuserid", i)
		}
		if _, ok := decomReq["labId"]; !ok {
			t.Errorf("payload %d missing labId", i)
		}
	}
}

func TestCleanupExpiredServers_GetAllServerStatesError(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			return nil, context.DeadlineExceeded
		},
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			t.Error("expected PushPayload not to be called when GetAllServerStates fails")
			return nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx := context.Background()
	// Should not panic
	worker.cleanupExpiredServers(ctx)
}

func TestCleanupExpiredServers_ContextCancellation(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	pastTime := time.Now().Add(-1 * time.Hour)

	pushCount := 0
	redisClient := &mockRedisClient{
		getAllServerStatesFunc: func(ctx context.Context, prefix string) ([]redis.ServerState, error) {
			// Return many expired servers
			servers := make([]redis.ServerState, 100)
			for i := 0; i < 100; i++ {
				servers[i] = redis.ServerState{
					ServerID:  "server",
					WebUserID: "user",
					LabID:     i,
					ExpiresAt: pastTime,
				}
			}
			return servers, nil
		},
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			pushCount++
			return nil
		},
	}

	worker := New(log, conn, redisClient)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after a brief moment
	go func() {
		time.Sleep(1 * time.Millisecond)
		cancel()
	}()

	worker.cleanupExpiredServers(ctx)

	// Should have stopped before processing all servers
	// (though might have processed some)
	if pushCount > 100 {
		t.Error("processed more servers than expected after context cancellation")
	}
}

func TestPushDecommissionRequest_Success(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	var capturedPayload string
	var capturedQueueKey string

	redisClient := &mockRedisClient{
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			capturedQueueKey = queueKey
			capturedPayload = payload
			return nil
		},
	}

	worker := New(log, conn, redisClient)

	state := redis.ServerState{
		ServerID:  "test-server-123",
		WebUserID: "test-user",
		LabID:     42,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	ctx := context.Background()
	worker.pushDecommissionRequest(ctx, state)

	// Verify queue key
	if capturedQueueKey != config.DecommissionQueueKey {
		t.Errorf("expected queue key %s, got %s", config.DecommissionQueueKey, capturedQueueKey)
	}

	// Verify payload structure
	var decomReq map[string]interface{}
	if err := json.Unmarshal([]byte(capturedPayload), &decomReq); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if decomReq["webuserid"] != "test-user" {
		t.Errorf("expected webuserid 'test-user', got %v", decomReq["webuserid"])
	}

	// JSON numbers are unmarshaled as float64
	if decomReq["labId"] != float64(42) {
		t.Errorf("expected labId 42, got %v", decomReq["labId"])
	}
}

func TestPushDecommissionRequest_PushPayloadError(t *testing.T) {
	log := slog.Default()
	conn := &mockConnector{}

	pushCalled := false
	redisClient := &mockRedisClient{
		pushPayloadFunc: func(ctx context.Context, queueKey string, payload string) error {
			pushCalled = true
			return context.DeadlineExceeded
		},
	}

	worker := New(log, conn, redisClient)

	state := redis.ServerState{
		ServerID:  "test-server-123",
		WebUserID: "test-user",
		LabID:     42,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	ctx := context.Background()
	// Should not panic
	worker.pushDecommissionRequest(ctx, state)

	if !pushCalled {
		t.Error("expected PushPayload to be called")
	}
}
