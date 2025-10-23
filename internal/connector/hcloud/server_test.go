package hcloud

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestNewServer(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	connector := &Connector{
		client: nil,
		dryrun: false,
		log:    logger,
	}

	t.Run("with IPv6", func(t *testing.T) {
		hcloudServer := &hcloud.Server{
			ID:   12345,
			Name: "test-server",
			PublicNet: hcloud.ServerPublicNet{
				IPv6: hcloud.ServerPublicNetIPv6{
					IP: mustParseIP(t, "2001:db8::"),
				},
			},
		}

		server := newServer(hcloudServer, connector, logger)

		if server.id != 12345 {
			t.Errorf("expected id 12345, got %d", server.id)
		}
		if server.name != "test-server" {
			t.Errorf("expected name 'test-server', got '%s'", server.name)
		}
		if server.ipv6 != "2001:db8::1" {
			t.Errorf("expected ipv6 '2001:db8::1', got '%s'", server.ipv6)
		}
		if server.connector != connector {
			t.Error("connector not set correctly")
		}
		if server.log != logger {
			t.Error("logger not set correctly")
		}
	})

	t.Run("without IPv6", func(t *testing.T) {
		hcloudServer := &hcloud.Server{
			ID:   12345,
			Name: "test-server",
			PublicNet: hcloud.ServerPublicNet{
				IPv6: hcloud.ServerPublicNetIPv6{
					IP: nil,
				},
			},
		}

		server := newServer(hcloudServer, connector, logger)

		if server.ipv6 != "" {
			t.Errorf("expected empty ipv6, got '%s'", server.ipv6)
		}
	})
}

func TestServer_GetID(t *testing.T) {
	server := &Server{id: 42}
	if got := server.GetID(); got != "42" {
		t.Errorf("GetID() = %v, want %v", got, "42")
	}
}

func TestServer_GetName(t *testing.T) {
	server := &Server{name: "test-server"}
	if got := server.GetName(); got != "test-server" {
		t.Errorf("GetName() = %v, want %v", got, "test-server")
	}
}

func TestServer_GetIPv6Address(t *testing.T) {
	server := &Server{ipv6: "2001:db8::1"}
	if got := server.GetIPv6Address(); got != "2001:db8::1" {
		t.Errorf("GetIPv6Address() = %v, want %v", got, "2001:db8::1")
	}
}

func TestServer_String(t *testing.T) {
	server := &Server{
		name: "test-server",
		ipv6: "2001:db8::1",
	}
	expected := "test-server [2001:db8::1]"
	if got := server.String(); got != expected {
		t.Errorf("String() = %v, want %v", got, expected)
	}
}

func TestIsResourceLockedError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "locked error lowercase",
			err:      errors.New("resource is locked"),
			expected: true,
		},
		{
			name:     "locked error uppercase",
			err:      errors.New("Resource is LOCKED"),
			expected: true,
		},
		{
			name:     "locked keyword alone",
			err:      errors.New("locked"),
			expected: true,
		},
		{
			name:     "locked in message",
			err:      errors.New("operation failed: server locked by another process"),
			expected: true,
		},
		{
			name:     "resource is locked phrase",
			err:      errors.New("cannot proceed: resource is locked"),
			expected: true,
		},
		{
			name:     "non-locked error",
			err:      errors.New("server not found"),
			expected: false,
		},
		{
			name:     "empty error",
			err:      errors.New(""),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isResourceLockedError(tt.err); got != tt.expected {
				t.Errorf("isResourceLockedError() = %v, want %v for error: %v", got, tt.expected, tt.err)
			}
		})
	}
}

func TestParseServerID(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		expected    int64
		wantErr     bool
		errContains string
	}{
		{
			name:     "valid positive integer",
			id:       "12345",
			expected: 12345,
			wantErr:  false,
		},
		{
			name:     "valid single digit",
			id:       "1",
			expected: 1,
			wantErr:  false,
		},
		{
			name:     "valid large number",
			id:       "9223372036854775807", // max int64
			expected: 9223372036854775807,
			wantErr:  false,
		},
		{
			name:        "invalid - letters",
			id:          "abc123",
			wantErr:     true,
			errContains: "invalid server ID",
		},
		{
			name:        "invalid - empty",
			id:          "",
			wantErr:     true,
			errContains: "invalid server ID",
		},
		{
			name:        "invalid - float",
			id:          "123.45",
			wantErr:     true,
			errContains: "invalid server ID",
		},
		{
			name:     "invalid - negative",
			id:       "-123",
			expected: -123,
			wantErr:  false, // Note: function doesn't validate negative, just parses
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServerID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseServerID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing '%s', got '%v'", tt.errContains, err)
				}
			}
			if !tt.wantErr && got != tt.expected {
				t.Errorf("parseServerID() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// TestServer_GetState and TestServer_Delete would require mocking the hcloud client
// which is complex. These are better suited for integration tests.
// However, we can add table-driven tests for the logic if we refactor to use interfaces.

func TestServerInterface(t *testing.T) {
	// Verify that Server implements the connector.Server interface
	var _ interface {
		GetID() string
		GetName() string
		GetIPv6Address() string
		GetState() (string, error)
		Delete() error
		String() string
	} = (*Server)(nil)
}

// Helper function to parse IP addresses in tests
func mustParseIP(t *testing.T, ip string) net.IP {
	t.Helper()

	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("failed to parse IP %s", ip)
	}

	return parsed
}

// Mock tests for demonstrating behavior without actual API calls
func TestServer_Delete_DryRunBehavior(t *testing.T) {
	// This test demonstrates what would be tested with proper mocks
	// In a real scenario, we'd use a mock hcloud client
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Get server by ID
		// 2. If running, shutdown with retry logic for locked resources
		// 3. Wait for server to stop
		// 4. Delete server with retry logic for locked resources
		// 5. Log success

		// With proper mocks, we would test:
		// - Retry logic with exponential backoff
		// - Handling of locked resource errors
		// - Timeout handling in waitForStatus
		// - Error propagation
	})
}

func TestServer_WaitForStatus_Logic(t *testing.T) {
	// This test documents the waitForStatus logic
	// With proper mocks, we would test:
	// - Successful status transition
	// - Timeout after deadline
	// - Error handling from GetState
	// - Ticker behavior (checking every 5 seconds)
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Set deadline based on timeout
		// 2. Create ticker for 5-second intervals
		// 3. Loop until deadline:
		//    a. Get current state
		//    b. If matches expected, return nil
		//    c. Wait for next tick
		// 4. Return timeout error if deadline exceeded
	})
}

// Example of what a properly mocked test would look like
type mockHCloudClient struct {
	serverGetByID  func(ctx context.Context, id int64) (*hcloud.Server, *hcloud.Response, error)
	serverShutdown func(ctx context.Context, server *hcloud.Server) (*hcloud.Action, *hcloud.Response, error)
	serverDelete   func(ctx context.Context, server *hcloud.Server) (*hcloud.Action, *hcloud.Response, error)
}

func TestExampleMockPattern(t *testing.T) {
	t.Run("example of how to test with mocks", func(t *testing.T) {
		// This demonstrates the pattern for future mock-based tests
		// We would create a mock client, inject it into the connector,
		// and verify the expected interactions

		// Example mock setup:
		// mockClient := &mockHCloudClient{
		//     serverGetByID: func(ctx context.Context, id int64) (*hcloud.Server, *hcloud.Response, error) {
		//         return &hcloud.Server{ID: id, Status: hcloud.ServerStatusRunning}, nil, nil
		//     },
		// }

		t.Skip("This is a documentation test showing mock patterns")
	})
}
