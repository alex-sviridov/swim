package scaleway

import (
	"fmt"
	"os"

	"github.com/alex-sviridov/swim/internal/connector"
)

// ProvisionRequest contains parameters for provisioning a new server
type ProvisionRequest struct {
	ServerName        string
	ServerType        string
	SecurityGroupName string
	ImageID           string
	WebUsername       string
	WebLabID          int
	CloudInitFile     string // path to cloud-init file
	TTLMinutes        int    // time to live in minutes
}

func (r *ProvisionRequest) Validate() error {
	var missing []string

	if r.ServerName == "" {
		missing = append(missing, "ServerName")
	}
	if r.ServerType == "" {
		missing = append(missing, "ServerType")
	}
	if r.SecurityGroupName == "" {
		missing = append(missing, "SecurityGroupName")
	}
	if r.ImageID == "" {
		missing = append(missing, "ImageID")
	}
	if r.WebUsername == "" {
		missing = append(missing, "WebUsername")
	}
	if r.WebLabID == 0 {
		missing = append(missing, "WebLabID")
	}
	if r.TTLMinutes == 0 {
		missing = append(missing, "TTLMinutes")
	}
	if r.CloudInitFile == "" {
		missing = append(missing, "CloudInitFile")
	} else {
		// Check if the file exists and can be read
		if _, err := os.Stat(r.CloudInitFile); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("CloudInitFile does not exist: %s", r.CloudInitFile)
			}
			return fmt.Errorf("CloudInitFile cannot be accessed: %w", err)
		}
		// Try to open the file to verify read permissions
		f, err := os.Open(r.CloudInitFile)
		if err != nil {
			return fmt.Errorf("CloudInitFile cannot be read: %w", err)
		}
		f.Close()
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %v", missing)
	}
	return nil
}

var _ connector.ProvisionRequest = (*ProvisionRequest)(nil)
