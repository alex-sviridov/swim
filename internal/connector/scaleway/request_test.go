package scaleway

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUnmarshalAndValidate(t *testing.T) {
	// Create a temporary cloud-init file for testing
	tmpDir := t.TempDir()
	validCloudInitFile := filepath.Join(tmpDir, "cloud-init.yml")
	if err := os.WriteFile(validCloudInitFile, []byte("#cloud-config\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		req     ProvisionRequest
		wantErr bool
	}{
		{
			name: "valid request",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				LabID:             1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: false,
		},
		{
			name: "missing server type",
			req: ProvisionRequest{
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				LabID:             1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing security group name",
			req: ProvisionRequest{
				ServerType:    "DEV1-S",
				ImageID:       "ubuntu-jammy",
				WebUsername:   "testuser",
				LabID:         1,
				TTLMinutes:    60,
				CloudInitFile: validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing image id",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				WebUsername:       "testuser",
				LabID:             1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing web username",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				LabID:             1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing web lab id",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing ttl minutes",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				LabID:             1,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing cloud init file - now optional",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				LabID:             1,
				TTLMinutes:        60,
			},
			wantErr: false,
		},
		{
			name: "cloud init file does not exist",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				LabID:             1,
				TTLMinutes:        60,
				CloudInitFile:     "/nonexistent/file.yml",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal request to JSON payload
			payload, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatalf("failed to marshal request: %v", err)
			}

			// Test UnmarshalAndValidate
			req, err := UnmarshalAndValidate(string(payload))
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalAndValidate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// If no error expected, verify CloudInitContent was populated if CloudInitFile was provided
			if !tt.wantErr && tt.req.CloudInitFile != "" {
				if req.CloudInitContent == "" {
					t.Error("CloudInitContent should be populated when CloudInitFile is provided")
				}
				if req.CloudInitFile != "" {
					t.Error("CloudInitFile should be cleared after reading")
				}
			}

			// If no error expected, verify server name was generated
			if !tt.wantErr {
				if req.ServerName() == "" {
					t.Error("ServerName should be generated")
				}
				// Verify server name pattern: lab{num}-{8 letters}
				expected := len("lab") + len(fmt.Sprint(tt.req.LabID)) + 1 + 8 // lab + labID + - + 8 chars
				if len(req.ServerName()) != expected {
					t.Errorf("ServerName length = %d, want %d (pattern: lab%d-{8chars})", len(req.ServerName()), expected, tt.req.LabID)
				}
			}
		})
	}
}

func TestGenerateServerName(t *testing.T) {
	tests := []struct {
		name  string
		labID int
		want  string // prefix only, we'll check the pattern
	}{
		{"lab 1", 1, "lab1-"},
		{"lab 42", 42, "lab42-"},
		{"lab 999", 999, "lab999-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateServerName(tt.labID)

			// Check prefix
			if !startsWithPrefix(got, tt.want) {
				t.Errorf("generateServerName() = %v, want prefix %v", got, tt.want)
			}

			// Check total length (prefix + 8 chars)
			expectedLen := len(tt.want) + 8
			if len(got) != expectedLen {
				t.Errorf("generateServerName() length = %d, want %d", len(got), expectedLen)
			}

			// Check suffix is all lowercase letters
			suffix := got[len(tt.want):]
			for _, c := range suffix {
				if c < 'a' || c > 'z' {
					t.Errorf("generateServerName() suffix contains non-lowercase letter: %c", c)
				}
			}
		})
	}
}

func startsWithPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
