package scaleway

import (
	"fmt"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
)

type Server struct {
	id        string
	name      string
	ipv6      string
	connector *Connector
}

func newServer(server *instance.Server, conn *Connector) *Server {
	var ipv6 string
	if len(server.PublicIPs) > 0 {
		ipv6 = server.PublicIPs[0].Address.String()
	}
	return &Server{
		id:        server.ID,
		name:      server.Name,
		ipv6:      ipv6,
		connector: conn,
	}
}

func (s *Server) GetID() string {
	return s.id
}

func (s *Server) GetName() string {
	return s.name
}

func (s *Server) GetIPv6Address() string {
	return s.ipv6
}

func (s *Server) GetState() (string, error) {
	resp, err := s.connector.instanceApi.GetServer(&instance.GetServerRequest{
		Zone:     s.connector.defaultZone,
		ServerID: s.id,
	})
	if err != nil {
		return "", err
	}
	return resp.Server.StateDetail, nil
}

func (s *Server) Delete() error {
	// First, power off the server if it's running
	_, err := s.connector.instanceApi.ServerAction(&instance.ServerActionRequest{
		Zone:     s.connector.defaultZone,
		ServerID: s.id,
		Action:   instance.ServerActionPoweroff,
	})
	// Ignore error if server is already stopped
	if err != nil {
		// Continue with deletion anyway
	}

	// Delete the server
	err = s.connector.instanceApi.DeleteServer(&instance.DeleteServerRequest{
		Zone:     s.connector.defaultZone,
		ServerID: s.id,
	})
	return err
}

func (s *Server) String() string {
	return fmt.Sprintf("%v [%v]", s.name, s.ipv6)
}

var _ connector.Server = (*Server)(nil)
