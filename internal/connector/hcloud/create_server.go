package hcloud

import (
	"context"
	"fmt"
	"strconv"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// CreateServer is the internal implementation that creates a new Hetzner Cloud server
// This method uses hcloud-specific types and has no knowledge of the connector interface
func (c *Connector) CreateServer(payload string) (connector.Server, error) {
	// Unmarshal, validate, and transform cloud-init file to content
	req, err := UnmarshalAndValidate(payload)
	if err != nil {
		return nil, err
	}

	if c.dryrun {
		// Return a mock server for dry-run mode
		dryRunServer := &Server{
			id:        999999,
			name:      req.ServerName(),
			ipv6:      "2001:db8::1",
			connector: c,
			log:       c.log,
		}
		c.log.Info("[DRY-RUN] Would create server",
			"name", req.ServerName(),
			"type", req.ServerType,
			"firewall_id", req.FirewallID,
			"location", req.Location)
		return dryRunServer, nil
	}

	// Create the server
	serverID, err := c.createServer(*req)
	if err != nil {
		return nil, fmt.Errorf("create server: %w", err)
	}

	// Get server instance with IP information
	server, err := c.getServer(serverID)
	if err != nil {
		c.cleanupServer(serverID)
		return nil, fmt.Errorf("get server: %w", err)
	}

	return server, nil
}

// createServer creates a new server instance
func (c *Connector) createServer(req ProvisionRequest) (int64, error) {
	ctx := context.Background()

	// Get firewall if provided
	var firewalls []*hcloud.ServerCreateFirewall
	if req.FirewallID != "" {
		firewall, _, err := c.client.Firewall.Get(ctx, req.FirewallID)
		if err != nil {
			return 0, fmt.Errorf("get firewall: %w", err)
		}
		if firewall == nil {
			return 0, fmt.Errorf("firewall '%s' not found", req.FirewallID)
		}
		firewalls = []*hcloud.ServerCreateFirewall{{Firewall: *firewall}}
	}

	// Get SSH key
	sshKey, _, err := c.client.SSHKey.Get(ctx, req.SSHKey)
	if err != nil {
		return 0, fmt.Errorf("get ssh key: %w", err)
	}
	if sshKey == nil {
		return 0, fmt.Errorf("ssh key '%s' not found", req.SSHKey)
	}

	// Prepare server create options
	createOpts := hcloud.ServerCreateOpts{
		Name:             req.ServerName(),
		ServerType:       &hcloud.ServerType{Name: req.ServerType},
		Image:            &hcloud.Image{Name: req.ImageID},
		Location:         &hcloud.Location{Name: req.Location},
		StartAfterCreate: hcloud.Ptr(true),
		PublicNet:        &hcloud.ServerCreatePublicNet{EnableIPv6: true},
		UserData:         req.CloudInitContent,
		SSHKeys:          []*hcloud.SSHKey{sshKey},
		Labels: map[string]string{
			"type":    "ephymerical-lab-host",
			"webuser": req.WebUsername,
			"labid":   strconv.Itoa(req.LabID),
			"ttl":     strconv.Itoa(req.TTLMinutes),
		},
		Firewalls: firewalls,
	}

	c.log.Info("creating server",
		"name", req.ServerName(),
		"type", req.ServerType,
		"image", req.ImageID,
		"location", req.Location)

	result, _, err := c.client.Server.Create(ctx, createOpts)
	if err != nil {
		return 0, fmt.Errorf("create server: %w", err)
	}

	c.log.Info("server created successfully",
		"server_id", result.Server.ID,
		"server_name", result.Server.Name)

	return result.Server.ID, nil
}

// getServer retrieves the server with full details
func (c *Connector) getServer(serverID int64) (*Server, error) {
	ctx := context.Background()

	server, _, err := c.client.Server.GetByID(ctx, serverID)
	if err != nil {
		return nil, err
	}

	if server == nil {
		return nil, fmt.Errorf("server with ID %d not found", serverID)
	}

	return newServer(server, c, c.log), nil
}

// cleanupServer deletes a server (used for error cleanup)
func (c *Connector) cleanupServer(serverID int64) {
	ctx := context.Background()
	server, _, err := c.client.Server.GetByID(ctx, serverID)
	if err != nil {
		c.log.Error("failed to get server for cleanup", "server_id", serverID, "error", err)
		return
	}
	if server != nil {
		_, _, err = c.client.Server.DeleteWithResult(ctx, server)
		if err != nil {
			c.log.Error("failed to cleanup server", "server_id", serverID, "error", err)
		}
	}
}
