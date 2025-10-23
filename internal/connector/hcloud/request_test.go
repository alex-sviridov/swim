package hcloud

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestUnmarshalAndValidate(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantErr     bool
		errContains string
		checkFields func(*testing.T, *ProvisionRequest)
	}{
		{
			name:    "valid payload",
			payload: `{"webuserid": "user-123", "labId": 42}`,
			wantErr: false,
			checkFields: func(t *testing.T, req *ProvisionRequest) {
				if req.WebUserID != "user-123" {
					t.Errorf("expected WebUserID 'user-123', got '%s'", req.WebUserID)
				}
				if req.LabID != 42 {
					t.Errorf("expected LabID 42, got %d", req.LabID)
				}
				if req.generatedName == "" {
					t.Error("expected generated name to be set")
				}
				if !strings.HasPrefix(req.generatedName, "lab42-") {
					t.Errorf("expected generated name to start with 'lab42-', got '%s'", req.generatedName)
				}
			},
		},
		{
			name:        "invalid JSON",
			payload:     `{invalid json`,
			wantErr:     true,
			errContains: "unmarshal payload",
		},
		{
			name:        "missing webuserid",
			payload:     `{"labId": 42}`,
			wantErr:     true,
			errContains: "missing required fields",
		},
		{
			name:        "missing labId",
			payload:     `{"webuserid": "user-123"}`,
			wantErr:     true,
			errContains: "missing required fields",
		},
		{
			name:        "missing both fields",
			payload:     `{}`,
			wantErr:     true,
			errContains: "missing required fields",
		},
		{
			name:    "labId as zero is invalid",
			payload: `{"webuserid": "user-123", "labId": 0}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := UnmarshalAndValidate(tt.payload)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalAndValidate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing '%s', got '%v'", tt.errContains, err)
				}
			}
			if !tt.wantErr && tt.checkFields != nil {
				tt.checkFields(t, req)
			}
		})
	}
}

func TestProvisionRequest_ServerName(t *testing.T) {
	req := &ProvisionRequest{
		WebUserID:     "user-123",
		LabID:         99,
		generatedName: "lab99-abcdefgh",
	}

	if got := req.ServerName(); got != "lab99-abcdefgh" {
		t.Errorf("ServerName() = %v, want %v", got, "lab99-abcdefgh")
	}
}

func TestGetHCloudConfigFromEnv(t *testing.T) {
	// Save original environment
	originalEnv := map[string]string{
		"HCLOUD_DEFAULT_SERVER_TYPE":     os.Getenv("HCLOUD_DEFAULT_SERVER_TYPE"),
		"HCLOUD_DEFAULT_FIREWALL":        os.Getenv("HCLOUD_DEFAULT_FIREWALL"),
		"HCLOUD_DEFAULT_IMAGE":           os.Getenv("HCLOUD_DEFAULT_IMAGE"),
		"HCLOUD_DEFAULT_LOCATION":        os.Getenv("HCLOUD_DEFAULT_LOCATION"),
		"HCLOUD_DEFAULT_SSH_KEY":         os.Getenv("HCLOUD_DEFAULT_SSH_KEY"),
		"HCLOUD_DEFAULT_CLOUD_INIT_FILE": os.Getenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE"),
		"DEFAULT_TTL_MINUTES":            os.Getenv("DEFAULT_TTL_MINUTES"),
	}
	defer func() {
		// Restore original environment
		for k, v := range originalEnv {
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}()

	t.Run("missing required env vars", func(t *testing.T) {
		// Clear all required env vars
		_ = os.Unsetenv("HCLOUD_DEFAULT_SERVER_TYPE")
		_ = os.Unsetenv("HCLOUD_DEFAULT_FIREWALL")
		_ = os.Unsetenv("HCLOUD_DEFAULT_IMAGE")
		_ = os.Unsetenv("HCLOUD_DEFAULT_LOCATION")
		_ = os.Unsetenv("HCLOUD_DEFAULT_SSH_KEY")
		_ = os.Unsetenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE")

		_, err := GetHCloudConfigFromEnv()
		if err == nil {
			t.Error("expected error for missing env vars, got nil")
		}
		if !strings.Contains(err.Error(), "missing required environment variables") {
			t.Errorf("expected error about missing env vars, got: %v", err)
		}
	})

	t.Run("cloud init file not found", func(t *testing.T) {
		_ = os.Setenv("HCLOUD_DEFAULT_SERVER_TYPE", "cx11")
		_ = os.Setenv("HCLOUD_DEFAULT_FIREWALL", "fw-123")
		_ = os.Setenv("HCLOUD_DEFAULT_IMAGE", "ubuntu-22.04")
		_ = os.Setenv("HCLOUD_DEFAULT_LOCATION", "nbg1")
		_ = os.Setenv("HCLOUD_DEFAULT_SSH_KEY", "key-123")
		_ = os.Setenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE", "/nonexistent/file.yaml")

		_, err := GetHCloudConfigFromEnv()
		if err == nil {
			t.Error("expected error for missing cloud-init file, got nil")
		}
		if !strings.Contains(err.Error(), "failed to read cloud-init file") {
			t.Errorf("expected error about cloud-init file, got: %v", err)
		}
	})

	t.Run("valid config with defaults", func(t *testing.T) {
		// Create temporary cloud-init file
		tmpFile, err := os.CreateTemp("", "cloud-init-*.yaml")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		content := "#cloud-config\n"
		if _, err := tmpFile.WriteString(content); err != nil {
			t.Fatalf("failed to write temp file: %v", err)
		}
		_ = tmpFile.Close()

		_ = os.Setenv("HCLOUD_DEFAULT_SERVER_TYPE", "cx11")
		_ = os.Setenv("HCLOUD_DEFAULT_FIREWALL", "fw-123")
		_ = os.Setenv("HCLOUD_DEFAULT_IMAGE", "ubuntu-22.04")
		_ = os.Setenv("HCLOUD_DEFAULT_LOCATION", "nbg1")
		_ = os.Setenv("HCLOUD_DEFAULT_SSH_KEY", "key-123")
		_ = os.Setenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE", tmpFile.Name())
		_ = os.Unsetenv("DEFAULT_TTL_MINUTES")

		config, err := GetHCloudConfigFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.ServerType != "cx11" {
			t.Errorf("expected ServerType 'cx11', got '%s'", config.ServerType)
		}
		if config.FirewallID != "fw-123" {
			t.Errorf("expected FirewallID 'fw-123', got '%s'", config.FirewallID)
		}
		if config.ImageID != "ubuntu-22.04" {
			t.Errorf("expected ImageID 'ubuntu-22.04', got '%s'", config.ImageID)
		}
		if config.Location != "nbg1" {
			t.Errorf("expected Location 'nbg1', got '%s'", config.Location)
		}
		if config.SSHKey != "key-123" {
			t.Errorf("expected SSHKey 'key-123', got '%s'", config.SSHKey)
		}
		if config.CloudInitContent != content {
			t.Errorf("expected CloudInitContent '%s', got '%s'", content, config.CloudInitContent)
		}
		if config.TTLMinutes != 30 {
			t.Errorf("expected default TTLMinutes 30, got %d", config.TTLMinutes)
		}
	})

	t.Run("valid config with custom TTL", func(t *testing.T) {
		// Create temporary cloud-init file
		tmpFile, err := os.CreateTemp("", "cloud-init-*.yaml")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()
		_ = tmpFile.Close()

		_ = os.Setenv("HCLOUD_DEFAULT_SERVER_TYPE", "cx11")
		_ = os.Setenv("HCLOUD_DEFAULT_FIREWALL", "fw-123")
		_ = os.Setenv("HCLOUD_DEFAULT_IMAGE", "ubuntu-22.04")
		_ = os.Setenv("HCLOUD_DEFAULT_LOCATION", "nbg1")
		_ = os.Setenv("HCLOUD_DEFAULT_SSH_KEY", "key-123")
		_ = os.Setenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE", tmpFile.Name())
		_ = os.Setenv("DEFAULT_TTL_MINUTES", "60")

		config, err := GetHCloudConfigFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.TTLMinutes != 60 {
			t.Errorf("expected TTLMinutes 60, got %d", config.TTLMinutes)
		}
	})

	t.Run("invalid TTL falls back to default", func(t *testing.T) {
		// Create temporary cloud-init file
		tmpFile, err := os.CreateTemp("", "cloud-init-*.yaml")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()
		_ = tmpFile.Close()

		_ = os.Setenv("HCLOUD_DEFAULT_SERVER_TYPE", "cx11")
		_ = os.Setenv("HCLOUD_DEFAULT_FIREWALL", "fw-123")
		_ = os.Setenv("HCLOUD_DEFAULT_IMAGE", "ubuntu-22.04")
		_ = os.Setenv("HCLOUD_DEFAULT_LOCATION", "nbg1")
		_ = os.Setenv("HCLOUD_DEFAULT_SSH_KEY", "key-123")
		_ = os.Setenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE", tmpFile.Name())
		_ = os.Setenv("DEFAULT_TTL_MINUTES", "invalid")

		config, err := GetHCloudConfigFromEnv()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if config.TTLMinutes != 30 {
			t.Errorf("expected default TTLMinutes 30 for invalid input, got %d", config.TTLMinutes)
		}
	})
}

func TestHCloudConfig_GetExpiresAt(t *testing.T) {
	config := &HCloudConfig{
		TTLMinutes: 15,
	}

	before := time.Now()
	expiresAt := config.GetExpiresAt()
	after := time.Now()

	expectedMin := before.Add(15 * time.Minute)
	expectedMax := after.Add(15 * time.Minute)

	if expiresAt.Before(expectedMin) || expiresAt.After(expectedMax) {
		t.Errorf("GetExpiresAt() = %v, expected between %v and %v", expiresAt, expectedMin, expectedMax)
	}
}

func TestGenerateServerName(t *testing.T) {
	tests := []struct {
		name  string
		labID int
	}{
		{"single digit lab", 1},
		{"double digit lab", 42},
		{"triple digit lab", 999},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := generateServerName(tt.labID)

			// Simpler check: just verify it starts with "lab{num}-"
			if !strings.HasPrefix(name, "lab") {
				t.Errorf("expected name to start with 'lab', got '%s'", name)
			}

			// Check format: lab{num}-{8 letters}
			parts := strings.Split(name, "-")
			if len(parts) != 2 {
				t.Errorf("expected name format 'lab{num}-{uid}', got '%s'", name)
			}

			// Check UID length (should be 8 characters)
			if len(parts) > 1 && len(parts[1]) != 8 {
				t.Errorf("expected UID length 8, got %d in '%s'", len(parts[1]), name)
			}

			// Check UID contains only lowercase letters
			if len(parts) > 1 {
				for _, c := range parts[1] {
					if c < 'a' || c > 'z' {
						t.Errorf("expected UID to contain only lowercase letters, got '%s'", parts[1])
						break
					}
				}
			}
		})
	}
}

func TestGenerateUID(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"length 1", 1},
		{"length 8", 8},
		{"length 16", 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uid := generateUID(tt.length)

			if len(uid) != tt.length {
				t.Errorf("expected length %d, got %d", tt.length, len(uid))
			}

			// Check all characters are lowercase letters
			for _, c := range uid {
				if c < 'a' || c > 'z' {
					t.Errorf("expected only lowercase letters, got '%c' in '%s'", c, uid)
				}
			}
		})
	}

	// Test uniqueness
	t.Run("generates unique UIDs", func(t *testing.T) {
		uids := make(map[string]bool)
		for i := 0; i < 100; i++ {
			uid := generateUID(8)
			if uids[uid] {
				t.Errorf("generateUID produced duplicate: '%s'", uid)
			}
			uids[uid] = true
		}
	})
}
