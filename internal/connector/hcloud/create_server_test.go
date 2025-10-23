package hcloud

import (
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestConnector_CreateServer_DryRun(t *testing.T) {
	// Save original environment
	originalToken := os.Getenv("HCLOUD_TOKEN")
	defer func() {
		if originalToken == "" {
			_ = os.Unsetenv("HCLOUD_TOKEN")
		} else {
			_ = os.Setenv("HCLOUD_TOKEN", originalToken)
		}
	}()

	_ = os.Setenv("HCLOUD_TOKEN", "test-token")
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("dry-run mode returns mock server", func(t *testing.T) {
		// Setup environment for config
		setupTestEnvironment(t)

		conn, err := NewConnector(logger, true) // dryrun=true
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		payload := `{"webuserid": "user-123", "labId": 42}`
		server, err := conn.CreateServer(payload)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if server == nil {
			t.Fatal("expected non-nil server")
		}

		// Verify mock server properties
		if server.GetID() != "999999" {
			t.Errorf("expected mock server ID '999999', got '%s'", server.GetID())
		}

		if !strings.HasPrefix(server.GetName(), "lab42-") {
			t.Errorf("expected server name to start with 'lab42-', got '%s'", server.GetName())
		}

		if server.GetIPv6Address() != "2001:db8::1" {
			t.Errorf("expected mock IPv6 '2001:db8::1', got '%s'", server.GetIPv6Address())
		}
	})

	t.Run("dry-run mode with invalid payload", func(t *testing.T) {
		setupTestEnvironment(t)

		conn, err := NewConnector(logger, true)
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		payload := `{"invalid": "payload"}`
		server, err := conn.CreateServer(payload)
		if err == nil {
			t.Error("expected error for invalid payload, got nil")
		}
		if server != nil {
			t.Error("expected nil server on error")
		}
	})

	t.Run("dry-run mode with missing config", func(t *testing.T) {
		// Clear environment variables
		_ = os.Unsetenv("HCLOUD_DEFAULT_SERVER_TYPE")
		_ = os.Unsetenv("HCLOUD_DEFAULT_FIREWALL")
		_ = os.Unsetenv("HCLOUD_DEFAULT_IMAGE")
		_ = os.Unsetenv("HCLOUD_DEFAULT_LOCATION")
		_ = os.Unsetenv("HCLOUD_DEFAULT_SSH_KEY")
		_ = os.Unsetenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE")

		conn, err := NewConnector(logger, true)
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		payload := `{"webuserid": "user-123", "labId": 42}`
		server, err := conn.CreateServer(payload)
		if err == nil {
			t.Error("expected error for missing config, got nil")
		}
		if server != nil {
			t.Error("expected nil server on error")
		}
		if !strings.Contains(err.Error(), "get hcloud config") {
			t.Errorf("expected error about config, got: %v", err)
		}
	})
}

func TestConnector_CreateServer_ExpectedBehavior(t *testing.T) {
	t.Run("documents expected behavior for non-dryrun mode", func(t *testing.T) {
		// Expected behavior:
		// 1. Unmarshal and validate payload
		// 2. Get HCloud configuration from environment
		// 3. If dryrun, return mock server
		// 4. Otherwise:
		//    a. Call createServer() to create actual server
		//    b. Call getServer() to get full server details
		//    c. If getServer fails, cleanup the created server
		//    d. Return server instance

		// With proper mocks, we would test:
		// - Successful server creation flow
		// - Cleanup on getServer failure
		// - Error propagation from createServer
		// - Correct parameters passed to hcloud API
		// - Proper handling of firewall, SSH keys, labels, etc.
	})
}

func TestConnector_createServer_ExpectedBehavior(t *testing.T) {
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Get firewall by ID (if provided)
		// 2. Get SSH key by name
		// 3. Build ServerCreateOpts with:
		//    - Server name from request
		//    - Server type, image, location from config
		//    - Start after create = true
		//    - Enable IPv6
		//    - UserData from cloud-init file
		//    - SSH keys
		//    - Labels: type, webuserid, labid, ttl
		//    - Firewalls
		// 4. Call client.Server.Create()
		// 5. Return server ID

		// With proper mocks, we would test:
		// - Correct ServerCreateOpts construction
		// - Firewall not found error handling
		// - SSH key not found error handling
		// - API create error handling
		// - Correct labels are set
		// - Logging of server creation
	})
}

func TestConnector_getServer_ExpectedBehavior(t *testing.T) {
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Call client.Server.GetByID() with server ID
		// 2. If server is nil, return "not found" error
		// 3. Call newServer() to create Server instance
		// 4. Return server

		// With proper mocks, we would test:
		// - Server found returns correct instance
		// - Server not found returns error
		// - API error is propagated
		// - newServer() is called with correct parameters
	})
}

func TestConnector_cleanupServer_ExpectedBehavior(t *testing.T) {
	t.Run("documents expected behavior", func(t *testing.T) {
		// Expected behavior:
		// 1. Get server by ID
		// 2. If found, delete server
		// 3. Log errors but don't return them (best effort cleanup)

		// With proper mocks, we would test:
		// - Server found and deleted successfully
		// - GetByID error is logged
		// - Delete error is logged
		// - No panics on nil server
	})
}

// Helper function to setup test environment with valid config
func setupTestEnvironment(t *testing.T) {
	t.Helper()

	// Create temporary cloud-init file
	tmpFile, err := os.CreateTemp("", "cloud-init-test-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(tmpFile.Name()) })

	content := "#cloud-config\npackages:\n  - docker\n"
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	_ = tmpFile.Close()

	// Set environment variables
	_ = os.Setenv("HCLOUD_DEFAULT_SERVER_TYPE", "cx11")
	_ = os.Setenv("HCLOUD_DEFAULT_FIREWALL", "fw-test")
	_ = os.Setenv("HCLOUD_DEFAULT_IMAGE", "ubuntu-22.04")
	_ = os.Setenv("HCLOUD_DEFAULT_LOCATION", "nbg1")
	_ = os.Setenv("HCLOUD_DEFAULT_SSH_KEY", "key-test")
	_ = os.Setenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE", tmpFile.Name())
	_ = os.Setenv("DEFAULT_TTL_MINUTES", "30")

	t.Cleanup(func() {
		_ = os.Unsetenv("HCLOUD_DEFAULT_SERVER_TYPE")
		_ = os.Unsetenv("HCLOUD_DEFAULT_FIREWALL")
		_ = os.Unsetenv("HCLOUD_DEFAULT_IMAGE")
		_ = os.Unsetenv("HCLOUD_DEFAULT_LOCATION")
		_ = os.Unsetenv("HCLOUD_DEFAULT_SSH_KEY")
		_ = os.Unsetenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE")
		_ = os.Unsetenv("DEFAULT_TTL_MINUTES")
	})
}

// Integration tests (only run when HCLOUD_TOKEN is set)
func TestConnector_CreateServer_Integration(t *testing.T) {
	if os.Getenv("HCLOUD_TOKEN") == "" {
		t.Skip("Skipping integration test: HCLOUD_TOKEN not set")
	}

	// Check if all required environment variables are set
	requiredVars := []string{
		"HCLOUD_DEFAULT_SERVER_TYPE",
		"HCLOUD_DEFAULT_FIREWALL",
		"HCLOUD_DEFAULT_IMAGE",
		"HCLOUD_DEFAULT_LOCATION",
		"HCLOUD_DEFAULT_SSH_KEY",
		"HCLOUD_DEFAULT_CLOUD_INIT_FILE",
	}

	for _, v := range requiredVars {
		if os.Getenv(v) == "" {
			t.Skipf("Skipping integration test: %s not set", v)
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("create and delete server", func(t *testing.T) {
		conn, err := NewConnector(logger, false)
		if err != nil {
			t.Fatalf("failed to create connector: %v", err)
		}

		// Create server
		payload := `{"webuserid": "test-user", "labId": 999}`
		server, err := conn.CreateServer(payload)
		if err != nil {
			t.Fatalf("failed to create server: %v", err)
		}

		t.Logf("Created server: ID=%s, Name=%s, IPv6=%s",
			server.GetID(), server.GetName(), server.GetIPv6Address())

		// Ensure cleanup
		defer func() {
			if err := server.Delete(); err != nil {
				t.Logf("Warning: failed to cleanup server: %v", err)
			}
		}()

		// Verify server properties
		if server.GetID() == "" {
			t.Error("expected non-empty server ID")
		}
		if !strings.HasPrefix(server.GetName(), "lab999-") {
			t.Errorf("expected server name to start with 'lab999-', got '%s'", server.GetName())
		}

		// Try to get server by ID
		retrievedServer, err := conn.GetServerByID(server.GetID())
		if err != nil {
			t.Fatalf("failed to get server by ID: %v", err)
		}

		if retrievedServer.GetID() != server.GetID() {
			t.Errorf("expected server ID %s, got %s", server.GetID(), retrievedServer.GetID())
		}
	})
}

// Example of what a properly mocked test would look like
func TestExampleMockCreateServer(t *testing.T) {
	t.Run("example showing mock pattern for CreateServer", func(t *testing.T) {
		// This demonstrates how future tests could be structured with proper mocks

		// Example mock setup:
		// mockFirewallAPI := &mockFirewallAPI{
		//     getFunc: func(idOrName string) (*hcloud.Firewall, error) {
		//         return &hcloud.Firewall{ID: 1, Name: "test-fw"}, nil
		//     },
		// }
		// mockSSHKeyAPI := &mockSSHKeyAPI{
		//     getFunc: func(idOrName string) (*hcloud.SSHKey, error) {
		//         return &hcloud.SSHKey{ID: 1, Name: "test-key"}, nil
		//     },
		// }
		// mockServerAPI := &mockServerAPI{
		//     createFunc: func(opts hcloud.ServerCreateOpts) (*hcloud.ServerCreateResult, error) {
		//         return &hcloud.ServerCreateResult{
		//             Server: &hcloud.Server{ID: 12345, Name: opts.Name},
		//         }, nil
		//     },
		// }

		// Then we would:
		// 1. Inject these mocks into the connector
		// 2. Call CreateServer with test payload
		// 3. Verify the correct API methods were called
		// 4. Verify the ServerCreateOpts had correct values
		// 5. Verify the returned server has expected properties

		t.Skip("This is a documentation test showing mock patterns")
	})
}

// Benchmark for CreateServer in dryrun mode
func BenchmarkConnector_CreateServer_DryRun(b *testing.B) {
	_ = os.Setenv("HCLOUD_TOKEN", "benchmark-token")
	defer func() { _ = os.Unsetenv("HCLOUD_TOKEN") }()

	// Setup environment
	tmpFile, err := os.CreateTemp("", "cloud-init-bench-*.yaml")
	if err != nil {
		b.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, _ = tmpFile.WriteString("#cloud-config\n")
	_ = tmpFile.Close()

	_ = os.Setenv("HCLOUD_DEFAULT_SERVER_TYPE", "cx11")
	_ = os.Setenv("HCLOUD_DEFAULT_FIREWALL", "fw-bench")
	_ = os.Setenv("HCLOUD_DEFAULT_IMAGE", "ubuntu-22.04")
	_ = os.Setenv("HCLOUD_DEFAULT_LOCATION", "nbg1")
	_ = os.Setenv("HCLOUD_DEFAULT_SSH_KEY", "key-bench")
	_ = os.Setenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE", tmpFile.Name())

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	conn, err := NewConnector(logger, true)
	if err != nil {
		b.Fatalf("failed to create connector: %v", err)
	}

	payload := `{"webuserid": "bench-user", "labId": 123}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := conn.CreateServer(payload)
		if err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}
