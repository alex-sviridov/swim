package scaleway

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
)

// ProvisionRequest contains parameters for provisioning a new server
type ProvisionRequest struct {
	ServerType        string
	SecurityGroupName string
	ImageID           string
	WebUsername       string
	LabID             int
	CloudInitFile     string // path to cloud-init file (for unmarshaling)
	CloudInitContent  string // cloud-init content as text
	TTLMinutes        int    // time to live in minutes
	generatedName     string // generated server name (not from JSON)
}

// UnmarshalAndValidate unmarshals JSON payload into ProvisionRequest, validates it,
// and transforms CloudInitFile path to CloudInitContent text
func UnmarshalAndValidate(payload string) (*ProvisionRequest, error) {
	var req ProvisionRequest

	// Unmarshal the payload
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	// Validate required fields
	var missing []string

	if req.ServerType == "" {
		req.ServerType = os.Getenv("SWIM_PROVISION_DEFAULT_INSTANCE_TYPE")
		if req.ServerType == "" {
			missing = append(missing, "ServerType")
		}
	}
	if req.SecurityGroupName == "" {
		req.SecurityGroupName = os.Getenv("SWIM_PROVISION_DEFAULT_SECURITY_GROUP")
		if req.SecurityGroupName == "" {
			missing = append(missing, "SecurityGroupName")
		}
	}
	if req.ImageID == "" {
		req.ImageID = os.Getenv("SWIM_PROVISION_DEFAULT_IMAGE_ID")
		if req.ImageID == "" {
			missing = append(missing, "ImageID")
		}
	}
	if req.WebUsername == "" {
		missing = append(missing, "WebUsername")
	}
	if req.LabID == 0 {
		missing = append(missing, "LabID")
	}
	if req.TTLMinutes == 0 {
		missing = append(missing, "TTLMinutes")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required fields: %v", missing)
	}

	// Generate server name
	req.generatedName = generateServerName(req.LabID)

	// Transform CloudInitFile to CloudInitContent
	if req.CloudInitFile != "" {
		content, err := os.ReadFile(req.CloudInitFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read cloud-init file: %w", err)
		}
		req.CloudInitContent = string(content)
		req.CloudInitFile = "" // Clear the file path after reading
	}

	return &req, nil
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
