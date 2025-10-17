package scaleway

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProvisionRequest_Validate(t *testing.T) {
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
				ServerName:        "test-server",
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				WebLabID:          1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: false,
		},
		{
			name: "missing server name",
			req: ProvisionRequest{
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				WebLabID:          1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing server type",
			req: ProvisionRequest{
				ServerName:        "test-server",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				WebLabID:          1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing security group name",
			req: ProvisionRequest{
				ServerName:    "test-server",
				ServerType:    "DEV1-S",
				ImageID:       "ubuntu-jammy",
				WebUsername:   "testuser",
				WebLabID:      1,
				TTLMinutes:    60,
				CloudInitFile: validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing image id",
			req: ProvisionRequest{
				ServerName:        "test-server",
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				WebUsername:       "testuser",
				WebLabID:          1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing web username",
			req: ProvisionRequest{
				ServerName:        "test-server",
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebLabID:          1,
				TTLMinutes:        60,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing web lab id",
			req: ProvisionRequest{
				ServerName:        "test-server",
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
				ServerName:        "test-server",
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				WebLabID:          1,
				CloudInitFile:     validCloudInitFile,
			},
			wantErr: true,
		},
		{
			name: "missing cloud init file",
			req: ProvisionRequest{
				ServerName:        "test-server",
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				WebLabID:          1,
				TTLMinutes:        60,
			},
			wantErr: true,
		},
		{
			name: "cloud init file does not exist",
			req: ProvisionRequest{
				ServerName:        "test-server",
				ServerType:        "DEV1-S",
				SecurityGroupName: "default",
				ImageID:           "ubuntu-jammy",
				WebUsername:       "testuser",
				WebLabID:          1,
				TTLMinutes:        60,
				CloudInitFile:     "/nonexistent/file.yml",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ProvisionRequest.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
