package decommissioner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

// mockConnectorServer implements the connector.Server interface for testing.
type mockConnectorServer struct {
	id          string
	name        string // Added to satisfy connector.Server interface
	ipv6        string // Added to satisfy connector.Server interface
	state       string // Added to satisfy connector.Server interface
	deleteErr   error
	deleteCalls int
}

func (m *mockConnectorServer) GetID() string {
	return m.id
}

// GetName implements connector.Server.GetName
func (m *mockConnectorServer) GetName() string {
	return m.name
}

// GetIPv6Address implements connector.Server.GetIPv6Address
func (m *mockConnectorServer) GetIPv6Address() string {
	return m.ipv6
}

// GetState implements connector.Server.GetState
func (m *mockConnectorServer) GetState() (string, error) {
	return m.state, nil // Simple mock state
}

// String implements connector.Server.String
func (m *mockConnectorServer) String() string {
	return "MockServer{id=" + m.id + ", name=" + m.name + ", ipv6=" + m.ipv6 + ", state=" + m.state + "}"
}

func (m *mockConnectorServer) Delete() error {
	m.deleteCalls++
	return m.deleteErr
}

// mockConnector implements the connector.Connector interface for testing.
type mockConnector struct {
	servers   map[string]*mockConnectorServer
	getErr    error
	getCalls  map[string]int
	lastGetID string
}

func newMockConnector() *mockConnector {
	return &mockConnector{
		servers:  make(map[string]*mockConnectorServer),
		getCalls: make(map[string]int),
	}
}

func (m *mockConnector) GetServerByID(id string) (connector.Server, error) {
	m.getCalls[id]++
	m.lastGetID = id
	if m.getErr != nil {
		return nil, m.getErr
	}
	server, ok := m.servers[id]
	if !ok {
		return nil, errors.New("server not found")
	}
	return server, nil
}

// CreateServer implements connector.Connector.CreateServer
func (m *mockConnector) CreateServer(payload string) (connector.Server, error) {
	// For decommissioning tests, we typically don't call CreateServer,
	// but it needs to be implemented to satisfy the interface.
	// We can return a dummy server or an error if we want to explicitly test
	// that it's not called. For now, a dummy server.
	id := "mock-created-server"
	server := m.addServer(id, nil)
	return server, nil
}

// ListServers implements connector.Connector.ListServers
func (m *mockConnector) ListServers() ([]connector.Server, error) {
	servers := make([]connector.Server, 0, len(m.servers))
	for _, s := range m.servers {
		servers = append(servers, s)
	}
	return servers, nil
}

func (m *mockConnector) addServer(id string, deleteErr error) *mockConnectorServer {
	server := &mockConnectorServer{
		id:        id,
		name:      "mock-server-name-" + id,
		ipv6:      "2001:db8::" + id,
		state:     "running", // Default state for mock
		deleteErr: deleteErr,
	}
	m.servers[id] = server
	return server
}

// mockRedisClient implements the redis.ClientInterface for testing.
type mockRedisClient struct {
	states       map[string]redis.ServerState
	getErr       error
	pushErr      error
	deleteErr    error
	pushedStates map[string]redis.ServerState
	deletedKeys  []string
}

func newMockRedisClient() *mockRedisClient {
	return &mockRedisClient{
		states:       make(map[string]redis.ServerState),
		pushedStates: make(map[string]redis.ServerState),
	}
}

func (m *mockRedisClient) GetServerState(ctx context.Context, key string) (*redis.ServerState, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	state, ok := m.states[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return &state, nil
}

func (m *mockRedisClient) PushServerState(ctx context.Context, key string, state redis.ServerState, ttl time.Duration) error {
	// Always record the state that was *attempted* to be pushed
	m.pushedStates[key] = state
	if m.pushErr != nil {
		return m.pushErr
	}
	return nil
}

func (m *mockRedisClient) DeleteServerState(ctx context.Context, key string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deletedKeys = append(m.deletedKeys, key)
	return nil
}

// PushPayload implements redis.ClientInterface.PushPayload
func (m *mockRedisClient) PushPayload(ctx context.Context, key string, payload string) error {
	// For decommissioning tests, this method is not directly asserted, so a no-op is fine.
	return nil
}

// PopPayload implements redis.ClientInterface.PopPayload
func (m *mockRedisClient) PopPayload(ctx context.Context, key string, timeout time.Duration) (string, error) {
	// This method is not directly used by the Decommissioner's ProcessRequest,
	// but it's required to satisfy the redis.ClientInterface.
	return "", nil
}

// GetAllServerStates implements redis.ClientInterface.GetAllServerStates
func (m *mockRedisClient) GetAllServerStates(ctx context.Context, prefix string) ([]redis.ServerState, error) {
	states := make([]redis.ServerState, 0, len(m.states))
	for _, s := range m.states {
		states = append(states, s)
	}
	return states, nil
}

func (m *mockRedisClient) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	// Allow by default in tests (not rate limited)
	return true, nil
}

// Close implements redis.ClientInterface.Close
func (m *mockRedisClient) Close() error {
	return nil // No-op for mock
}

func (m *mockRedisClient) addState(key string, state redis.ServerState) {
	m.states[key] = state
}

func TestProcessRequest(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	baseState := redis.ServerState{
		ServerID:  "server-123",
		WebUserID: "user-abc",
		LabID:     5,
		Status:    config.StatusRunning,
	}
	cacheKey := redis.ServerCacheKey("user-abc")

	tests := []struct {
		name               string
		payload            string
		setupRedis         func(*mockRedisClient)
		setupConnector     func(*mockConnector)
		expectDeleteCall   bool
		expectRedisDelete  bool
		expectRedisPush    bool
		expectedPushStatus string
	}{
		{
			name:               "happy path with matching labId",
			payload:            `{"webuserid":"user-abc", "labId": 5}`,
			setupRedis:         func(r *mockRedisClient) { r.addState(cacheKey, baseState) },
			setupConnector:     func(c *mockConnector) { c.addServer("server-123", nil) },
			expectDeleteCall:   true,
			expectRedisDelete:  true,
			expectRedisPush:    true,
			expectedPushStatus: config.StatusStopping,
		},
		{
			name:               "happy path without labId",
			payload:            `{"webuserid":"user-abc"}`,
			setupRedis:         func(r *mockRedisClient) { r.addState(cacheKey, baseState) },
			setupConnector:     func(c *mockConnector) { c.addServer("server-123", nil) },
			expectDeleteCall:   true,
			expectRedisDelete:  true,
			expectRedisPush:    true,
			expectedPushStatus: config.StatusStopping,
		},
		{
			name:              "labId mismatch should ignore request",
			payload:           `{"webuserid":"user-abc", "labId": 99}`,
			setupRedis:        func(r *mockRedisClient) { r.addState(cacheKey, baseState) },
			setupConnector:    func(c *mockConnector) {},
			expectDeleteCall:  false,
			expectRedisDelete: false,
			expectRedisPush:   false,
		},
		{
			name:              "server not found in cache",
			payload:           `{"webuserid":"user-abc"}`,
			setupRedis:        func(r *mockRedisClient) {},
			setupConnector:    func(c *mockConnector) {},
			expectDeleteCall:  false,
			expectRedisDelete: false,
			expectRedisPush:   false,
		},
		{
			name:               "server already deleted from provider",
			payload:            `{"webuserid":"user-abc"}`,
			setupRedis:         func(r *mockRedisClient) { r.addState(cacheKey, baseState) },
			setupConnector:     func(c *mockConnector) {}, // No server in connector
			expectDeleteCall:   false,
			expectRedisDelete:  true,
			expectRedisPush:    true,
			expectedPushStatus: config.StatusStopping,
		},
		{
			name:               "provider delete fails",
			payload:            `{"webuserid":"user-abc"}`,
			setupRedis:         func(r *mockRedisClient) { r.addState(cacheKey, baseState) },
			setupConnector:     func(c *mockConnector) { c.addServer("server-123", errors.New("api error")) },
			expectDeleteCall:   true,
			expectRedisDelete:  false, // Should not delete from cache if provider fails
			expectRedisPush:    true,
			expectedPushStatus: config.StatusStopping,
		},
		{
			name:              "invalid json payload",
			payload:           `{"webuserid":"user-abc", "labId": 5`,
			setupRedis:        func(r *mockRedisClient) {},
			setupConnector:    func(c *mockConnector) {},
			expectDeleteCall:  false,
			expectRedisDelete: false,
			expectRedisPush:   false,
		},
		{
			name:              "missing webuserid",
			payload:           `{"labId": 5}`,
			setupRedis:        func(r *mockRedisClient) {},
			setupConnector:    func(c *mockConnector) {},
			expectDeleteCall:  false,
			expectRedisDelete: false,
			expectRedisPush:   false,
		},
		{
			name:    "redis push status fails",
			payload: `{"webuserid":"user-abc"}`,
			setupRedis: func(r *mockRedisClient) {
				r.addState(cacheKey, baseState)
				r.pushErr = errors.New("redis push failed")
			},
			setupConnector:     func(c *mockConnector) { c.addServer("server-123", nil) },
			expectDeleteCall:   true,
			expectRedisDelete:  true,
			expectRedisPush:    true, // Push is attempted
			expectedPushStatus: config.StatusStopping,
		},
		{
			name:    "redis delete fails after provider delete",
			payload: `{"webuserid":"user-abc"}`,
			setupRedis: func(r *mockRedisClient) { // Corrected type here
				r.addState(cacheKey, baseState)
				r.deleteErr = errors.New("redis delete failed")
			},
			setupConnector:     func(c *mockConnector) { c.addServer("server-123", nil) },
			expectDeleteCall:   true,
			expectRedisDelete:  false, // Delete is attempted but fails
			expectRedisPush:    true,
			expectedPushStatus: config.StatusStopping,
		},
		{
			name:    "cache-less deletion with serverID in payload",
			payload: `{"webuserid":"user-xyz", "serverId":"orphaned-server-999"}`,
			setupRedis: func(r *mockRedisClient) {
				// No state in cache - simulates cache entry being replaced
			},
			setupConnector: func(c *mockConnector) {
				// But server exists in cloud provider
				c.addServer("orphaned-server-999", nil)
			},
			expectDeleteCall:  true,
			expectRedisDelete: false, // No cache entry to delete
			expectRedisPush:   false, // No cache to push "stopping" status to
		},
		{
			name:    "cache-less deletion with serverID - server already deleted from cloud",
			payload: `{"webuserid":"user-xyz", "serverId":"already-deleted-server"}`,
			setupRedis: func(r *mockRedisClient) {
				// No state in cache
			},
			setupConnector: func(c *mockConnector) {
				// Server not in cloud either
			},
			expectDeleteCall:  false, // GetServerByID returns error
			expectRedisDelete: false,
			expectRedisPush:   false,
		},
		{
			name:    "cache miss without serverID in payload - aborts",
			payload: `{"webuserid":"user-notfound"}`,
			setupRedis: func(r *mockRedisClient) {
				// No state in cache
			},
			setupConnector: func(c *mockConnector) {
				// Has server but no serverID in payload to find it
				c.addServer("unknown-server", nil)
			},
			expectDeleteCall:  false,
			expectRedisDelete: false,
			expectRedisPush:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			mockRedis := newMockRedisClient()
			if tt.setupRedis != nil {
				tt.setupRedis(mockRedis)
			}

			mockConn := newMockConnector()
			if tt.setupConnector != nil {
				tt.setupConnector(mockConn)
			}

			decomm := New(log, mockConn, mockRedis)

			// Run the method
			decomm.ProcessRequest(ctx, tt.payload)

			// Assertions
			var req DecommissionRequest
			_ = json.Unmarshal([]byte(tt.payload), &req)

			// Determine which serverID to check - from payload or from baseState
			serverID := baseState.ServerID
			if req.ServerID != "" {
				serverID = req.ServerID
			}

			// Check if connector's Delete was called
			var deleteCalls int
			if server, ok := mockConn.servers[serverID]; ok {
				deleteCalls = server.deleteCalls
			}
			if tt.expectDeleteCall && deleteCalls == 0 {
				t.Errorf("expected connector.Delete to be called, but it wasn't")
			}
			if !tt.expectDeleteCall && deleteCalls > 0 {
				t.Errorf("expected connector.Delete not to be called, but it was")
			}

			// Check if redis state was pushed
			_, pushed := mockRedis.pushedStates[cacheKey]
			if tt.expectRedisPush && !pushed {
				t.Errorf("expected redis.PushServerState to be called, but it wasn't")
			}
			if !tt.expectRedisPush && pushed {
				t.Errorf("expected redis.PushServerState not to be called, but it was")
			}
			// Only check the status if a push was expected.
			if tt.expectRedisPush && pushed {
				if mockRedis.pushedStates[cacheKey].Status != tt.expectedPushStatus {
					t.Errorf("expected pushed status to be '%s', got '%s'", tt.expectedPushStatus, mockRedis.pushedStates[cacheKey].Status)
				}
			}

			// Check if redis state was deleted
			deleted := false
			for _, key := range mockRedis.deletedKeys {
				if key == cacheKey {
					deleted = true
					break
				}
			}
			if tt.expectRedisDelete && !deleted {
				t.Errorf("expected redis.DeleteServerState to be called, but it wasn't")
			}
			if !tt.expectRedisDelete && deleted {
				t.Errorf("expected redis.DeleteServerState not to be called, but it was")
			}
		})
	}
}
