package scaleway

import (
	"fmt"
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
	if r.CloudInitFile == "" {
		missing = append(missing, "CloudInitFile")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required fields: %v", missing)
	}
	return nil
}

var _ connector.ProvisionRequest = (*ProvisionRequest)(nil)
