package redis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// redisOperations is an interface that abstracts the redis.Client operations we use
type redisOperations interface {
	BLPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd
	RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd
	Get(ctx context.Context, key string) *redis.StringCmd
	Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd
	Del(ctx context.Context, keys ...string) *redis.IntCmd
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	Close() error
}

// mockRedisOps is a mock implementation of redisOperations
type mockRedisOps struct {
	// BLPop mock
	blpopResult []string
	blpopErr    error

	// RPush mock
	rpushErr error

	// Set mock
	setErr error

	// Get mock
	getResult  string
	getErr     error
	getResults map[string]string
	getErrors  map[string]error

	// Scan mock
	scanKeys []string
	scanErr  error

	// Del mock
	delErr error

	// SetNX mock
	setnxResult bool
	setnxErr    error

	// Close mock
	closeErr error
}

func (m *mockRedisOps) BLPop(ctx context.Context, timeout time.Duration, keys ...string) *redis.StringSliceCmd {
	cmd := redis.NewStringSliceCmd(ctx)
	if m.blpopErr != nil {
		cmd.SetErr(m.blpopErr)
	} else {
		cmd.SetVal(m.blpopResult)
	}
	return cmd
}

func (m *mockRedisOps) RPush(ctx context.Context, key string, values ...interface{}) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	if m.rpushErr != nil {
		cmd.SetErr(m.rpushErr)
	}
	return cmd
}

func (m *mockRedisOps) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(ctx)
	if m.setErr != nil {
		cmd.SetErr(m.setErr)
	}
	return cmd
}

func (m *mockRedisOps) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(ctx)

	// Check for specific key errors
	if m.getErrors != nil {
		if err, ok := m.getErrors[key]; ok {
			cmd.SetErr(err)
			return cmd
		}
	}

	// Check for specific key results
	if m.getResults != nil {
		if result, ok := m.getResults[key]; ok {
			cmd.SetVal(result)
			return cmd
		}
	}

	// Fall back to default
	if m.getErr != nil {
		cmd.SetErr(m.getErr)
	} else {
		cmd.SetVal(m.getResult)
	}
	return cmd
}

func (m *mockRedisOps) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	cmd := redis.NewScanCmd(ctx, nil)
	if m.scanErr != nil {
		cmd.SetErr(m.scanErr)
	} else {
		// Return keys and cursor 0 (indicating no more keys)
		cmd.SetVal(m.scanKeys, 0)
	}
	return cmd
}

func (m *mockRedisOps) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	if m.delErr != nil {
		cmd.SetErr(m.delErr)
	}
	return cmd
}

func (m *mockRedisOps) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	cmd := redis.NewBoolCmd(ctx)
	if m.setnxErr != nil {
		cmd.SetErr(m.setnxErr)
	} else {
		cmd.SetVal(m.setnxResult)
	}
	return cmd
}

func (m *mockRedisOps) Close() error {
	return m.closeErr
}

// testClient wraps our mock operations
type testClient struct {
	ops redisOperations
}

func (c *testClient) PopPayload(ctx context.Context, queueKey string, timeout time.Duration) (string, error) {
	result, err := c.ops.BLPop(ctx, timeout, queueKey).Result()
	if err != nil {
		if err == redis.Nil {
			return "", errors.New("no payload available in queue")
		}
		return "", err
	}

	if len(result) < 2 {
		return "", errors.New("unexpected response from Redis")
	}

	return result[1], nil
}

func (c *testClient) PushPayload(ctx context.Context, queueKey string, payload string) error {
	return c.ops.RPush(ctx, queueKey, payload).Err()
}

func (c *testClient) PushServerState(ctx context.Context, cacheKey string, state ServerState, ttl time.Duration) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return c.ops.Set(ctx, cacheKey, data, ttl).Err()
}

func (c *testClient) GetServerState(ctx context.Context, cacheKey string) (*ServerState, error) {
	data, err := c.ops.Get(ctx, cacheKey).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, errors.New("server state not found in cache")
		}
		return nil, err
	}

	var state ServerState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return nil, err
	}

	return &state, nil
}

func (c *testClient) GetAllServerStates(ctx context.Context, prefix string) ([]ServerState, error) {
	var states []ServerState

	iter := c.ops.Scan(ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		state, err := c.GetServerState(ctx, key)
		if err != nil {
			continue
		}
		states = append(states, *state)
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	return states, nil
}

func (c *testClient) DeleteServerState(ctx context.Context, cacheKey string) error {
	return c.ops.Del(ctx, cacheKey).Err()
}

func (c *testClient) TryAcquireRateLimit(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	key := RateLimitKey(webUserID, operation)
	success, err := c.ops.SetNX(ctx, key, "1", ttl).Result()
	if err != nil {
		return false, err
	}
	return success, nil
}

func (c *testClient) Close() error {
	return c.ops.Close()
}

// Test functions
func TestPopPayload_Success(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			blpopResult: []string{"queue:provision", `{"webuserid":"user123","labId":42}`},
			blpopErr:    nil,
		},
	}

	ctx := context.Background()
	payload, err := client.PopPayload(ctx, "queue:provision", 5*time.Second)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := `{"webuserid":"user123","labId":42}`
	if payload != expected {
		t.Errorf("expected payload %q, got %q", expected, payload)
	}
}

func TestPopPayload_NoPayloadAvailable(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			blpopErr: redis.Nil,
		},
	}

	ctx := context.Background()
	_, err := client.PopPayload(ctx, "queue:provision", 5*time.Second)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expectedMsg := "no payload available in queue"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestPopPayload_RedisError(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			blpopErr: errors.New("connection refused"),
		},
	}

	ctx := context.Background()
	_, err := client.PopPayload(ctx, "queue:provision", 5*time.Second)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "connection refused" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPopPayload_UnexpectedResponse(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			blpopResult: []string{"only-one-element"},
			blpopErr:    nil,
		},
	}

	ctx := context.Background()
	_, err := client.PopPayload(ctx, "queue:provision", 5*time.Second)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expectedMsg := "unexpected response from Redis"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestPushPayload_Success(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			rpushErr: nil,
		},
	}

	ctx := context.Background()
	payload := `{"webuserid":"user123","labId":42}`
	err := client.PushPayload(ctx, "queue:provision", payload)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPushPayload_Error(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			rpushErr: errors.New("connection error"),
		},
	}

	ctx := context.Background()
	err := client.PushPayload(ctx, "queue:provision", "test-payload")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "connection error" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPushServerState_Success(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			setErr: nil,
		},
	}

	ctx := context.Background()
	state := ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      "running",
		Available:   true,
		CloudStatus: "running",
		ServerID:    "server-123",
		ExpiresAt:   time.Now().Add(30 * time.Minute),
		WebUserID:   "user123",
		LabID:       42,
	}

	err := client.PushServerState(ctx, "vmmanager:servers:user123", state, 30*time.Minute)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestPushServerState_SetError(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			setErr: errors.New("redis connection error"),
		},
	}

	ctx := context.Background()
	state := ServerState{
		User:      "student",
		ServerID:  "server-123",
		WebUserID: "user123",
		LabID:     42,
	}

	err := client.PushServerState(ctx, "vmmanager:servers:user123", state, 30*time.Minute)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "redis connection error" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetServerState_Success(t *testing.T) {
	expectedState := ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      "running",
		Available:   true,
		CloudStatus: "running",
		ServerID:    "server-123",
		ExpiresAt:   time.Now().Add(30 * time.Minute),
		WebUserID:   "user123",
		LabID:       42,
	}

	data, _ := json.Marshal(expectedState)

	client := &testClient{
		ops: &mockRedisOps{
			getResult: string(data),
			getErr:    nil,
		},
	}

	ctx := context.Background()
	state, err := client.GetServerState(ctx, "vmmanager:servers:user123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if state.User != expectedState.User {
		t.Errorf("expected User %q, got %q", expectedState.User, state.User)
	}
	if state.Address != expectedState.Address {
		t.Errorf("expected Address %q, got %q", expectedState.Address, state.Address)
	}
	if state.ServerID != expectedState.ServerID {
		t.Errorf("expected ServerID %q, got %q", expectedState.ServerID, state.ServerID)
	}
	if state.LabID != expectedState.LabID {
		t.Errorf("expected LabID %d, got %d", expectedState.LabID, state.LabID)
	}
}

func TestGetServerState_NotFound(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			getErr: redis.Nil,
		},
	}

	ctx := context.Background()
	_, err := client.GetServerState(ctx, "vmmanager:servers:nonexistent")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	expectedMsg := "server state not found in cache"
	if err.Error() != expectedMsg {
		t.Errorf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestGetServerState_RedisError(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			getErr: errors.New("connection timeout"),
		},
	}

	ctx := context.Background()
	_, err := client.GetServerState(ctx, "vmmanager:servers:user123")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "connection timeout" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetServerState_UnmarshalError(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			getResult: "invalid json {{{",
			getErr:    nil,
		},
	}

	ctx := context.Background()
	_, err := client.GetServerState(ctx, "vmmanager:servers:user123")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Error message contains "invalid character"
	if err.Error()[:17] != "invalid character" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetAllServerStates_Success(t *testing.T) {
	state1 := ServerState{
		User:      "student",
		ServerID:  "server-1",
		WebUserID: "user1",
		LabID:     1,
	}
	state2 := ServerState{
		User:      "student",
		ServerID:  "server-2",
		WebUserID: "user2",
		LabID:     2,
	}

	data1, _ := json.Marshal(state1)
	data2, _ := json.Marshal(state2)

	client := &testClient{
		ops: &mockRedisOps{
			scanKeys: []string{"vmmanager:servers:user1", "vmmanager:servers:user2"},
			getResults: map[string]string{
				"vmmanager:servers:user1": string(data1),
				"vmmanager:servers:user2": string(data2),
			},
		},
	}

	ctx := context.Background()
	states, err := client.GetAllServerStates(ctx, "vmmanager:servers:")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d", len(states))
	}

	// Check that both states are present
	foundUser1 := false
	foundUser2 := false
	for _, state := range states {
		if state.WebUserID == "user1" && state.ServerID == "server-1" {
			foundUser1 = true
		}
		if state.WebUserID == "user2" && state.ServerID == "server-2" {
			foundUser2 = true
		}
	}

	if !foundUser1 {
		t.Error("expected to find state for user1")
	}
	if !foundUser2 {
		t.Error("expected to find state for user2")
	}
}

func TestGetAllServerStates_EmptyResult(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			scanKeys: []string{},
		},
	}

	ctx := context.Background()
	states, err := client.GetAllServerStates(ctx, "vmmanager:servers:")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(states) != 0 {
		t.Errorf("expected 0 states, got %d", len(states))
	}
}

func TestGetAllServerStates_ScanError(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			scanErr: errors.New("scan failed"),
		},
	}

	ctx := context.Background()
	_, err := client.GetAllServerStates(ctx, "vmmanager:servers:")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "scan failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetAllServerStates_SkipsInvalidStates(t *testing.T) {
	state1 := ServerState{
		User:      "student",
		ServerID:  "server-1",
		WebUserID: "user1",
		LabID:     1,
	}

	data1, _ := json.Marshal(state1)

	client := &testClient{
		ops: &mockRedisOps{
			scanKeys: []string{"vmmanager:servers:user1", "vmmanager:servers:user2"},
			getResults: map[string]string{
				"vmmanager:servers:user1": string(data1),
				// user2 will return error
			},
			getErrors: map[string]error{
				"vmmanager:servers:user2": errors.New("invalid state"),
			},
		},
	}

	ctx := context.Background()
	states, err := client.GetAllServerStates(ctx, "vmmanager:servers:")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Should only return the valid state
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}

	if states[0].WebUserID != "user1" {
		t.Errorf("expected user1, got %s", states[0].WebUserID)
	}
}

func TestDeleteServerState_Success(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			delErr: nil,
		},
	}

	ctx := context.Background()
	err := client.DeleteServerState(ctx, "vmmanager:servers:user123")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDeleteServerState_Error(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			delErr: errors.New("delete failed"),
		},
	}

	ctx := context.Background()
	err := client.DeleteServerState(ctx, "vmmanager:servers:user123")

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "delete failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClose_Success(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			closeErr: nil,
		},
	}

	err := client.Close()

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestClose_Error(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			closeErr: errors.New("close failed"),
		},
	}

	err := client.Close()

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "close failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestServerState_JSONMarshalUnmarshal(t *testing.T) {
	originalState := ServerState{
		User:        "student",
		Address:     "2001:db8::1",
		Status:      "running",
		Available:   true,
		CloudStatus: "running",
		ServerID:    "server-123",
		ExpiresAt:   time.Now().Round(time.Second),
		WebUserID:   "user123",
		LabID:       42,
	}

	// Marshal
	data, err := json.Marshal(originalState)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Unmarshal
	var unmarshaledState ServerState
	if err := json.Unmarshal(data, &unmarshaledState); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Compare fields
	if unmarshaledState.User != originalState.User {
		t.Errorf("User mismatch: got %q, want %q", unmarshaledState.User, originalState.User)
	}
	if unmarshaledState.Address != originalState.Address {
		t.Errorf("Address mismatch: got %q, want %q", unmarshaledState.Address, originalState.Address)
	}
	if unmarshaledState.Status != originalState.Status {
		t.Errorf("Status mismatch: got %q, want %q", unmarshaledState.Status, originalState.Status)
	}
	if unmarshaledState.Available != originalState.Available {
		t.Errorf("Available mismatch: got %v, want %v", unmarshaledState.Available, originalState.Available)
	}
	if unmarshaledState.CloudStatus != originalState.CloudStatus {
		t.Errorf("CloudStatus mismatch: got %q, want %q", unmarshaledState.CloudStatus, originalState.CloudStatus)
	}
	if unmarshaledState.ServerID != originalState.ServerID {
		t.Errorf("ServerID mismatch: got %q, want %q", unmarshaledState.ServerID, originalState.ServerID)
	}
	if unmarshaledState.WebUserID != originalState.WebUserID {
		t.Errorf("WebUserID mismatch: got %q, want %q", unmarshaledState.WebUserID, originalState.WebUserID)
	}
	if unmarshaledState.LabID != originalState.LabID {
		t.Errorf("LabID mismatch: got %d, want %d", unmarshaledState.LabID, originalState.LabID)
	}
	if !unmarshaledState.ExpiresAt.Equal(originalState.ExpiresAt) {
		t.Errorf("ExpiresAt mismatch: got %v, want %v", unmarshaledState.ExpiresAt, originalState.ExpiresAt)
	}
}

// Rate limit tests
func TestTryAcquireRateLimit_Success(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			setnxResult: true,
			setnxErr:    nil,
		},
	}

	ctx := context.Background()
	allowed, err := client.TryAcquireRateLimit(ctx, "user123", "provision", 15*time.Second)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !allowed {
		t.Error("expected rate limit to be acquired (allowed=true)")
	}
}

func TestTryAcquireRateLimit_RateLimited(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			setnxResult: false, // Key already exists
			setnxErr:    nil,
		},
	}

	ctx := context.Background()
	allowed, err := client.TryAcquireRateLimit(ctx, "user123", "provision", 15*time.Second)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if allowed {
		t.Error("expected rate limit to be hit (allowed=false)")
	}
}

func TestTryAcquireRateLimit_RedisError(t *testing.T) {
	client := &testClient{
		ops: &mockRedisOps{
			setnxErr: errors.New("connection failed"),
		},
	}

	ctx := context.Background()
	_, err := client.TryAcquireRateLimit(ctx, "user123", "provision", 15*time.Second)

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "connection failed" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRateLimitKey(t *testing.T) {
	tests := []struct {
		name      string
		userID    string
		operation string
		expected  string
	}{
		{
			name:      "provision operation",
			userID:    "user123",
			operation: "provision",
			expected:  "vmmanager:ratelimit:user123:provision",
		},
		{
			name:      "decommission operation",
			userID:    "user456",
			operation: "decommission",
			expected:  "vmmanager:ratelimit:user456:decommission",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := RateLimitKey(tt.userID, tt.operation)
			if key != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, key)
			}
		})
	}
}
