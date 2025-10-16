package scaleway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

// CreateServer is the internal implementation that creates a new Scaleway server
// This method uses scaleway-specific types and has no knowledge of the connector interface
func (c *Connector) CreateServer(payload string) (connector.Server, error) {
	req := ProvisionRequest{}

	// Unmarshal the payload
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		return nil, err
	}

	// Validate the request
	if err := req.Validate(); err != nil {
		return nil, err
	}

	// Step 1: Find security group by name
	securityGroup, err := c.findSecurityGroup(req.SecurityGroupName)
	if err != nil {
		return nil, fmt.Errorf("failed to find security group: %w", err)
	}

	if c.dryrun {
		return nil, nil
	}

	// Step 2: Create the server
	serverID, err := c.createServer(req, securityGroup)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	// Step 3: Attach routed IPv6
	if err := c.attachRoutedIPv6(serverID); err != nil {
		return nil, fmt.Errorf("failed to attach IPv6: %w", err)
	}

	// Step 4: Get server instance
	server, err := c.getServer(serverID)
	if err != nil {
		return nil, fmt.Errorf("failed to get server IP: %w", err)
	}

	// Step 5: Upload cloud-init if provided
	if req.CloudInitFile != "" {
		if err := c.uploadCloudInit(serverID, req.CloudInitFile); err != nil {
			return nil, fmt.Errorf("failed to upload cloud-init: %w", err)
		}
	}

	// Step 6: Power on the server
	if err := c.powerOnServer(serverID); err != nil {
		return nil, fmt.Errorf("failed to power on server: %w", err)
	}

	// print server.StateDetail
	state, err := server.GetState()
	if err != nil {
		return nil, fmt.Errorf("failed to get server state: %w", err)
	}
	fmt.Printf("Server state: %s\n", state)

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
		Name:              req.ServerName,
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
		return newServer(resp.Server, c), nil
	}

	return nil, fmt.Errorf("no public IP found for server")
}

// uploadCloudInit uploads cloud-init user data from a file
func (c *Connector) uploadCloudInit(serverID, cloudInitFile string) error {
	content, err := os.ReadFile(cloudInitFile)
	if err != nil {
		return fmt.Errorf("failed to read cloud-init file: %w", err)
	}

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
