package scaleway

import (
	"fmt"
	"os"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

type Connector struct {
	client      *scw.Client
	instanceApi *instance.API
	dryrun      bool
	projectID   string
	defaultZone scw.Zone
}

func NewConnector(dryrun bool) (c *Connector, err error) {
	accessKey := os.Getenv("SCW_ACCESS_KEY")
	secretKey := os.Getenv("SCW_SECRET_KEY")
	organizationID := os.Getenv("SCW_ORGANIZATION_ID")
	projectID := os.Getenv("SCW_PROJECT_ID")
	defaultZone := os.Getenv("SCW_DEFAULT_ZONE")

	var missing []string
	if accessKey == "" {
		missing = append(missing, "SCW_ACCESS_KEY")
	}
	if secretKey == "" {
		missing = append(missing, "SCW_SECRET_KEY")
	}
	if organizationID == "" {
		missing = append(missing, "SCW_ORGANIZATION_ID")
	}
	if projectID == "" {
		missing = append(missing, "SCW_PROJECT_ID")
	}
	if defaultZone == "" {
		missing = append(missing, "SCW_DEFAULT_ZONE")
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}

	c = &Connector{
		projectID:   projectID,
		defaultZone: scw.Zone(defaultZone),
		dryrun:      dryrun,
	}

	// Create a Scaleway client
	c.client, err = scw.NewClient(
		scw.WithDefaultOrganizationID(organizationID),
		scw.WithAuth(accessKey, secretKey),
		scw.WithDefaultZone(scw.Zone(defaultZone)),
	)
	if err != nil {
		return nil, err
	}
	c.instanceApi = instance.NewAPI(c.client)
	return c, nil
}

func (c *Connector) ListServers() (servers []connector.Server, err error) {
	response, err := c.instanceApi.ListServers(&instance.ListServersRequest{})
	if err != nil {
		return nil, err
	}
	for _, server := range response.Servers {
		s := newServer(server, c)
		servers = append(servers, s)
	}
	return servers, nil
}

func (c *Connector) GetServerByID(id string) (connector.Server, error) {
	resp, err := c.instanceApi.GetServer(&instance.GetServerRequest{
		Zone:     c.defaultZone,
		ServerID: id,
	})
	if err != nil {
		return nil, err
	}
	return newServer(resp.Server, c), nil
}

var _ connector.Connector = (*Connector)(nil)
