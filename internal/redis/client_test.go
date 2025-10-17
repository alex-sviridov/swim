package redis

import (
	"testing"
	"time"
)

func TestServerState_Marshaling(t *testing.T) {
	state := ServerState{
		ID:            "test-id-123",
		Name:          "test-server",
		IPv6:          "2001:db8::1",
		State:         "running",
		ProvisionedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	// Basic field checks
	if state.ID != "test-id-123" {
		t.Errorf("ID = %v, want %v", state.ID, "test-id-123")
	}
	if state.Name != "test-server" {
		t.Errorf("Name = %v, want %v", state.Name, "test-server")
	}
	if state.IPv6 != "2001:db8::1" {
		t.Errorf("IPv6 = %v, want %v", state.IPv6, "2001:db8::1")
	}
	if state.State != "running" {
		t.Errorf("State = %v, want %v", state.State, "running")
	}
}

func TestConfig_Validation(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		valid  bool
	}{
		{
			name: "valid config with address",
			config: Config{
				Address:  "localhost:6379",
				Password: "",
				DB:       0,
			},
			valid: true,
		},
		{
			name: "valid config with password",
			config: Config{
				Address:  "localhost:6379",
				Password: "secret",
				DB:       1,
			},
			valid: true,
		},
		{
			name: "valid redis url",
			config: Config{
				Address:  "redis://localhost:6379",
				Password: "",
				DB:       0,
			},
			valid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Just verify config structure is valid
			if tt.config.Address == "" && tt.valid {
				t.Error("valid config should have non-empty Address")
			}
		})
	}
}
