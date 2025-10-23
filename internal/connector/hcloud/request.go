package hcloud

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// ProvisionRequest contains parameters for provisioning a new server
// This is the minimal request format from LabMan
type ProvisionRequest struct {
	WebUserID     string `json:"webuserid"` // Keycloak user ID
	LabID         int    `json:"labId"`     // Lab ID
	generatedName string // generated server name (not from JSON)
}

// UnmarshalAndValidate unmarshals JSON payload into ProvisionRequest and validates it
func UnmarshalAndValidate(payload string) (*ProvisionRequest, error) {
	var req ProvisionRequest

	// Unmarshal the payload
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	// Validate required fields
	var missing []string
	if req.WebUserID == "" {
		missing = append(missing, "webuserid")
	}
	if req.LabID == 0 {
		missing = append(missing, "labId")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required fields: %v", missing)
	}

	// Generate server name
	req.generatedName = generateServerName(req.LabID)

	return &req, nil
}

// GetHCloudConfig returns Hetzner Cloud configuration from environment
type HCloudConfig struct {
	ServerType       string
	FirewallID       string
	ImageID          string
	Location         string
	SSHKey           string
	CloudInitFile    string
	CloudInitContent string
	TTLMinutes       int
}

// GetHCloudConfigFromEnv reads Hetzner Cloud configuration from environment
func GetHCloudConfigFromEnv() (*HCloudConfig, error) {
	var missing []string

	serverType := os.Getenv("HCLOUD_DEFAULT_SERVER_TYPE")
	if serverType == "" {
		missing = append(missing, "HCLOUD_DEFAULT_SERVER_TYPE")
	}

	firewallID := os.Getenv("HCLOUD_DEFAULT_FIREWALL")
	if firewallID == "" {
		missing = append(missing, "HCLOUD_DEFAULT_FIREWALL")
	}

	imageID := os.Getenv("HCLOUD_DEFAULT_IMAGE")
	if imageID == "" {
		missing = append(missing, "HCLOUD_DEFAULT_IMAGE")
	}

	location := os.Getenv("HCLOUD_DEFAULT_LOCATION")
	if location == "" {
		missing = append(missing, "HCLOUD_DEFAULT_LOCATION")
	}

	sshKey := os.Getenv("HCLOUD_DEFAULT_SSH_KEY")
	if sshKey == "" {
		missing = append(missing, "HCLOUD_DEFAULT_SSH_KEY")
	}

	cloudInitFile := os.Getenv("HCLOUD_DEFAULT_CLOUD_INIT_FILE")
	if cloudInitFile == "" {
		missing = append(missing, "HCLOUD_DEFAULT_CLOUD_INIT_FILE")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	// Read cloud-init file
	cloudInitContent, err := os.ReadFile(cloudInitFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cloud-init file: %w", err)
	}

	// Get TTL with default
	ttlMinutes := 30
	if ttlStr := os.Getenv("DEFAULT_TTL_MINUTES"); ttlStr != "" {
		if ttl, err := strconv.Atoi(ttlStr); err == nil {
			ttlMinutes = ttl
		}
	}

	return &HCloudConfig{
		ServerType:       serverType,
		FirewallID:       firewallID,
		ImageID:          imageID,
		Location:         location,
		SSHKey:           sshKey,
		CloudInitFile:    cloudInitFile,
		CloudInitContent: string(cloudInitContent),
		TTLMinutes:       ttlMinutes,
	}, nil
}

// GetExpiresAt calculates expiration time based on TTL
func (c *HCloudConfig) GetExpiresAt() time.Time {
	return time.Now().Add(time.Duration(c.TTLMinutes) * time.Minute)
}

// generateServerName creates a server name with pattern lab{num}-{8 letters UID}
func generateServerName(labID int) string {
	uid := generateUID(8)
	return fmt.Sprintf("lab%d-%s", labID, uid)
}

// generateUID generates a random string of n lowercase letters
func generateUID(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = letters[b[i]%26]
	}
	return string(b)
}

// ServerName returns the generated server name
func (r *ProvisionRequest) ServerName() string {
	return r.generatedName
}
