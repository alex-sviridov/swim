package hcloud

import (
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestNewConnector(t *testing.T) {
	// Save original environment
	originalToken := os.Getenv("HCLOUD_TOKEN")
	defer func() {
		if originalToken == "" {
			os.Unsetenv("HCLOUD_TOKEN")
		} else {
			os.Setenv("HCLOUD_TOKEN", originalToken)
		}
	}()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("missing HCLOUD_TOKEN", func(t *testing.T) {
		os.Unsetenv("HCLOUD_TOKEN")

		conn, err := NewConnector(logger, false)
		if err == nil {
			t.Error("expected error for missing HCLOUD_TOKEN, got nil")
		}
		if conn != nil {
			t.Error("expected nil connector when error occurs")
		}
		if !strings.Contains(err.Error(), "missing required environment variable: HCLOUD_TOKEN") {
			t.Errorf("expected error about missing HCLOUD_TOKEN, got: %v", err)
		}
	})

	t.Run("valid token non-dryrun", func(t *testing.T) {
		os.Setenv("HCLOUD_TOKEN", "test-token-123")

		conn, err := NewConnector(logger, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if conn == nil {
			t.Fatal("expected non-nil connector")
		}
		if conn.client == nil {
			t.Error("expected non-nil client")
		}
		if conn.dryrun != false {
			t.Error("expected dryrun to be false")
		}
		if conn.log != logger {
			t.Error("expected logger to be set")
		}
	})

	t.Run("valid token with dryrun", func(t *testing.T) {
		os.Setenv("HCLOUD_TOKEN", "test-token-123")

		conn, err := NewConnector(logger, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if conn == nil {
			t.Fatal("expected non-nil connector")
		}
		if conn.dryrun != true {
			t.Error("expected dryrun to be true")
		}
	})

	t.Run("empty token string", func(t *testing.T) {
		os.Setenv("HCLOUD_TOKEN", "")

		conn, err := NewConnector(logger, false)
		if err == nil {
			t.Error("expected error for empty HCLOUD_TOKEN, got nil")
		}
		if conn != nil {
			t.Error("expected nil connector when error occurs")
		}
	})
}

func TestConnectorInterface(t *testing.T) {
	// Verify that Connector implements a connector-like interface
	// The actual connector.Connector interface uses connector.Server type
	t.Run("implements connector methods", func(t *testing.T) {
		var c *Connector
		if c != nil {
			// These calls verify the methods exist with correct signatures
			_, _ = c.ListServers()
			_, _ = c.GetServerByID("")
			_, _ = c.CreateServer("")
		}
	})
}

// TestConnector_ListServers and TestConnector_GetServerByID would require
// mocking the hcloud client or using integration tests.
// Here we document the expected behavior:

func TestConnector_ListServers_ExpectedBehavior(t *testing.T) {
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Call client.Server.All(ctx) to get all servers
		// 2. For each hcloud.Server, call newServer() to create Server instance
		// 3. Return slice of connector.Server interfaces
		// 4. Return error if API call fails

		// With proper mocks, we would test:
		// - Empty server list returns empty slice
		// - Multiple servers are all converted correctly
		// - API errors are propagated
		// - newServer() is called for each server with correct parameters
	})
}

func TestConnector_GetServerByID_ExpectedBehavior(t *testing.T) {
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Parse server ID string to int64
		// 2. Call client.Server.GetByID(ctx, id) to get server
		// 3. If server is nil, return "server not found" error
		// 4. Call newServer() to create Server instance
		// 5. Return connector.Server interface

		// With proper mocks, we would test:
		// - Valid server ID returns correct server
		// - Invalid ID format returns parse error
		// - Non-existent server ID returns "not found" error
		// - API errors are propagated
		// - newServer() is called with correct parameters
	})
}

// Integration test helpers (to be run only when HCLOUD_TOKEN is set)
func TestConnector_Integration(t *testing.T) {
	if os.Getenv("HCLOUD_TOKEN") == "" {
		t.Skip("Skipping integration test: HCLOUD_TOKEN not set")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("can create connector and list servers", func(t *testing.T) {
		conn, err := NewConnector(logger, false)
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		servers, err := conn.ListServers()
		if err != nil {
			t.Fatalf("failed to list servers: %v", err)
		}

		t.Logf("Found %d servers", len(servers))

		// Basic validation
		for i, server := range servers {
			if server == nil {
				t.Errorf("server at index %d is nil", i)
				continue
			}

			id := server.GetID()
			name := server.GetName()
			ipv6 := server.GetIPv6Address()

			if id == "" {
				t.Errorf("server at index %d has empty ID", i)
			}
			if name == "" {
				t.Errorf("server at index %d has empty name", i)
			}

			t.Logf("Server %d: ID=%s, Name=%s, IPv6=%s", i, id, name, ipv6)
		}
	})

	t.Run("get non-existent server returns error", func(t *testing.T) {
		conn, err := NewConnector(logger, false)
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		// Use a very high ID that's unlikely to exist
		server, err := conn.GetServerByID("999999999")
		if err == nil {
			t.Error("expected error for non-existent server, got nil")
		}
		if server != nil {
			t.Error("expected nil server for non-existent ID")
		}
	})

	t.Run("invalid server ID returns error", func(t *testing.T) {
		conn, err := NewConnector(logger, false)
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		server, err := conn.GetServerByID("invalid-id")
		if err == nil {
			t.Error("expected error for invalid server ID, got nil")
		}
		if server != nil {
			t.Error("expected nil server for invalid ID")
		}
		if !strings.Contains(err.Error(), "invalid server ID") {
			t.Errorf("expected error about invalid ID, got: %v", err)
		}
	})
}

// Benchmark tests
func BenchmarkNewConnector(b *testing.B) {
	// Save and set token
	originalToken := os.Getenv("HCLOUD_TOKEN")
	os.Setenv("HCLOUD_TOKEN", "benchmark-token")
	defer func() {
		if originalToken == "" {
			os.Unsetenv("HCLOUD_TOKEN")
		} else {
			os.Setenv("HCLOUD_TOKEN", originalToken)
		}
	}()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := NewConnector(logger, false)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

func BenchmarkParseServerID(b *testing.B) {
	ids := []string{"1", "12345", "9223372036854775807"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, id := range ids {
			_, err := parseServerID(id)
			if err != nil {
				b.Fatalf("unexpected error for id %s: %v", id, err)
			}
		}
	}
}

// Example test showing the structure for mock-based tests
type mockServerAPI struct {
	allFunc     func() ([]*MockHCloudServer, error)
	getByIDFunc func(id int64) (*MockHCloudServer, error)
}

type MockHCloudServer struct {
	ID   int64
	Name string
}

func TestExampleMockBasedTest(t *testing.T) {
	t.Run("example showing mock pattern for ListServers", func(t *testing.T) {
		// This demonstrates how future tests could be structured with proper mocks

		// Example mock setup:
		// mockAPI := &mockServerAPI{
		//     allFunc: func() ([]*MockHCloudServer, error) {
		//         return []*MockHCloudServer{
		//             {ID: 1, Name: "server-1"},
		//             {ID: 2, Name: "server-2"},
		//         }, nil
		//     },
		// }

		// Then we would inject this mock into the connector and verify:
		// 1. The correct API methods are called
		// 2. The results are properly converted to connector.Server
		// 3. Errors are handled correctly

		t.Skip("This is a documentation test showing mock patterns")
	})
}
