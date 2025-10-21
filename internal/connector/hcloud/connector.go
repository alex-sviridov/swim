package hcloud

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type Connector struct {
	client *hcloud.Client
	dryrun bool
	log    *slog.Logger
}

func NewConnector(log *slog.Logger, dryrun bool) (*Connector, error) {
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("missing required environment variable: HCLOUD_TOKEN")
	}

	return &Connector{
		client: hcloud.NewClient(hcloud.WithToken(token)),
		dryrun: dryrun,
		log:    log,
	}, nil
}

func (c *Connector) ListServers() (servers []connector.Server, err error) {
	ctx := context.Background()
	hcloudServers, err := c.client.Server.All(ctx)
	if err != nil {
		return nil, err
	}
	for _, server := range hcloudServers {
		s := newServer(server, c, c.log)
		servers = append(servers, s)
	}
	return servers, nil
}

func (c *Connector) GetServerByID(id string) (connector.Server, error) {
	ctx := context.Background()
	idInt, err := parseServerID(id)
	if err != nil {
		return nil, err
	}

	server, _, err := c.client.Server.GetByID(ctx, idInt)
	if err != nil {
		return nil, err
	}
	if server == nil {
		return nil, fmt.Errorf("server with ID %s not found", id)
	}
	return newServer(server, c, c.log), nil
}

var _ connector.Connector = (*Connector)(nil)
