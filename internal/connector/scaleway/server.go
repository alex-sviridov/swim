package scaleway

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
)

type Server struct {
	id        string
	name      string
	ipv6      string
	connector *Connector
	log       *slog.Logger
}

func newServer(server *instance.Server, conn *Connector, log *slog.Logger) *Server {
	var ipv6 string
	if len(server.PublicIPs) > 0 {
		ipv6 = server.PublicIPs[0].Address.String()
	}
	return &Server{
		id:        server.ID,
		name:      server.Name,
		ipv6:      ipv6,
		connector: conn,
		log:       log,
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
	s.log.Info("deleting server", "server_id", s.id, "server_name", s.name)

	// First, power off the server if it's running
	s.log.Info("powering off server", "server_id", s.id)
	_, err := s.connector.instanceApi.ServerAction(&instance.ServerActionRequest{
		Zone:     s.connector.defaultZone,
		ServerID: s.id,
		Action:   instance.ServerActionPoweroff,
	})
	if err != nil {
		// If server is already stopped, ignore the error
		if !strings.Contains(err.Error(), "server should be running") {
			return fmt.Errorf("failed to power off server: %w", err)
		}
		s.log.Info("server already stopped", "server_id", s.id)
	}

	// Wait for server to be stopped before deleting
	s.log.Info("waiting for server to stop", "server_id", s.id)
	maxWait := 2 * time.Minute
	checkInterval := 5 * time.Second
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		state, err := s.GetState()
		if err != nil {
			return fmt.Errorf("failed to get server state: %w", err)
		}

		if state == "stopped" || state == "stopped in place" {
			s.log.Info("server stopped", "server_id", s.id, "state", state)
			break
		}

		time.Sleep(checkInterval)
	}

	// Delete the server
	s.log.Info("deleting server from scaleway", "server_id", s.id)
	err = s.connector.instanceApi.DeleteServer(&instance.DeleteServerRequest{
		Zone:     s.connector.defaultZone,
		ServerID: s.id,
	})
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}

	s.log.Info("server deleted successfully", "server_id", s.id, "server_name", s.name)
	return nil
}

func (s *Server) String() string {
	return fmt.Sprintf("%v [%v]", s.name, s.ipv6)
}

var _ connector.Server = (*Server)(nil)
