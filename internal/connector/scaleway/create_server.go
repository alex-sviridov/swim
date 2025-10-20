package scaleway

import (
	"bytes"
	"fmt"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// CreateServer is the internal implementation that creates a new Scaleway server
// This method uses scaleway-specific types and has no knowledge of the connector interface
func (c *Connector) CreateServer(payload string) (connector.Server, error) {
	// Unmarshal, validate, and transform cloud-init file to content
	req, err := UnmarshalAndValidate(payload)
	if err != nil {
		return nil, err
	}

	// Step 1: Find security group by name
	securityGroup, err := c.findSecurityGroup(req.SecurityGroupName)
	if err != nil {
		return nil, fmt.Errorf("find security group: %w", err)
	}

	if c.dryrun {
		// Return a mock server for dry-run mode
		dryRunServer := &Server{
			id:        "dry-run-server-id",
			name:      req.ServerName(),
			ipv6:      "2001:db8::1",
			connector: c,
		}
		fmt.Printf("[DRY-RUN] Would create server: %s (type: %s, security group: %s)\n",
			req.ServerName(), req.ServerType, securityGroup)
		return dryRunServer, nil
	}

	// Step 2: Create the server
	serverID, err := c.createServer(*req, securityGroup)
	if err != nil {
		return nil, fmt.Errorf("create server: %w", err)
	}

	// Step 3: Attach routed IPv6
	if err := c.attachRoutedIPv6(serverID); err != nil {
		c.cleanupServer(serverID)
		return nil, fmt.Errorf("attach ipv6: %w", err)
	}

	// Step 4: Get server instance
	server, err := c.getServer(serverID)
	if err != nil {
		c.cleanupServer(serverID)
		return nil, fmt.Errorf("get server ip: %w", err)
	}

	// Step 5: Upload cloud-init if provided
	if req.CloudInitContent != "" {
		if err := c.uploadCloudInitContent(serverID, []byte(req.CloudInitContent)); err != nil {
			c.cleanupServer(serverID)
			return nil, fmt.Errorf("upload cloud-init: %w", err)
		}
	}

	// Step 6: Power on the server
	if err := c.powerOnServer(serverID); err != nil {
		c.cleanupServer(serverID)
		return nil, fmt.Errorf("power on server: %w", err)
	}

	return server, nil
}

// findSecurityGroup looks up a security group by name
func (c *Connector) findSecurityGroup(name string) (string, error) {
	req := &instance.ListSecurityGroupsRequest{
		Zone:    c.defaultZone,
		Project: &c.projectID,
		Name:    &name,
	}

	resp, err := c.instanceApi.ListSecurityGroups(req)
	if err != nil {
		return "", err
	}

	if len(resp.SecurityGroups) == 0 {
		return "", fmt.Errorf("security group '%s' not found", name)
	}

	return resp.SecurityGroups[0].ID, nil
}

// createServer creates a new server instance
func (c *Connector) createServer(req ProvisionRequest, securityGroupID string) (string, error) {
	tags := []string{
		fmt.Sprintf("webuser:%s", req.WebUsername),
		fmt.Sprintf("labid:%d", req.WebLabID),
	}

	createReq := &instance.CreateServerRequest{
		Zone:              c.defaultZone,
		Name:              req.ServerName(),
		Project:           &c.projectID,
		CommercialType:    req.ServerType,
		Image:             &req.ImageID,
		Tags:              tags,
		DynamicIPRequired: scw.BoolPtr(false),
		SecurityGroup:     &securityGroupID,
	}

	resp, err := c.instanceApi.CreateServer(createReq)
	if err != nil {
		return "", err
	}

	return resp.Server.ID, nil
}

// attachRoutedIPv6 creates and attaches a routed IPv6 address to the server
func (c *Connector) attachRoutedIPv6(serverID string) error {
	req := &instance.CreateIPRequest{
		Zone:    c.defaultZone,
		Project: &c.projectID,
		Type:    instance.IPTypeRoutedIPv6,
		Server:  &serverID,
	}

	_, err := c.instanceApi.CreateIP(req)
	return err
}

// getServer retrieves the public IP address of the server
func (c *Connector) getServer(serverID string) (*Server, error) {
	req := &instance.GetServerRequest{
		Zone:     c.defaultZone,
		ServerID: serverID,
	}

	resp, err := c.instanceApi.GetServer(req)
	if err != nil {
		return nil, err
	}

	if len(resp.Server.PublicIPs) > 0 {
		return newServer(resp.Server, c, c.log), nil
	}

	return nil, fmt.Errorf("no public IP found for server")
}

// uploadCloudInitContent uploads cloud-init user data from memory
func (c *Connector) uploadCloudInitContent(serverID string, content []byte) error {
	req := &instance.SetServerUserDataRequest{
		Zone:     c.defaultZone,
		ServerID: serverID,
		Key:      "cloud-init",
		Content:  bytes.NewReader(content),
	}

	return c.instanceApi.SetServerUserData(req)
}

// powerOnServer starts the server
func (c *Connector) powerOnServer(serverID string) error {
	req := &instance.ServerActionRequest{
		Zone:     c.defaultZone,
		ServerID: serverID,
		Action:   instance.ServerActionPoweron,
	}

	_, err := c.instanceApi.ServerAction(req)
	return err
}

// cleanupServer deletes a server (used for error cleanup)
func (c *Connector) cleanupServer(serverID string) {
	deleteReq := &instance.DeleteServerRequest{
		Zone:     c.defaultZone,
		ServerID: serverID,
	}
	_ = c.instanceApi.DeleteServer(deleteReq)
}
