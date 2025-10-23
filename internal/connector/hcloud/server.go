package hcloud

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type Server struct {
	id        int64
	name      string
	ipv6      string
	connector *Connector
	log       *slog.Logger
}

func newServer(server *hcloud.Server, conn *Connector, log *slog.Logger) *Server {
	var ipv6 string
	if server.PublicNet.IPv6.IP != nil {
		// Hetzner provides IPv6 as /64 subnet, append ::1 for the actual host address
		ipv6 = server.PublicNet.IPv6.IP.String() + "1"
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
	return strconv.FormatInt(s.id, 10)
}

func (s *Server) GetName() string {
	return s.name
}

func (s *Server) GetIPv6Address() string {
	return s.ipv6
}

// isResourceLockedError checks if an error is due to a locked resource
func isResourceLockedError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "locked") || strings.Contains(errMsg, "resource is locked")
}

func (s *Server) GetState() (string, error) {
	ctx := context.Background()
	server, _, err := s.connector.client.Server.GetByID(ctx, s.id)
	if err != nil {
		return "", err
	}
	if server == nil {
		return "", fmt.Errorf("server with ID %d not found", s.id)
	}
	return string(server.Status), nil
}

func (s *Server) Delete() error {
	ctx := context.Background()
	s.log.Info("deleting server", "server_id", s.id, "server_name", s.name)

	server, _, err := s.connector.client.Server.GetByID(ctx, s.id)
	if err != nil {
		return fmt.Errorf("get server: %w", err)
	}
	if server == nil {
		return fmt.Errorf("server with ID %d not found", s.id)
	}

	// Shutdown if running
	if server.Status == hcloud.ServerStatusRunning {
		s.log.Info("shutting down server", "server_id", s.id)

		// Retry shutdown with exponential backoff if resource is locked
		retryDelay := config.InitialRetryDelay
		var shutdownErr error
		for attempt := 1; attempt <= config.MaxRetryAttempts; attempt++ {
			_, _, shutdownErr = s.connector.client.Server.Shutdown(ctx, server)
			if shutdownErr == nil {
				break
			}

			if isResourceLockedError(shutdownErr) {
				s.log.Warn("server is locked, retrying shutdown",
					"server_id", s.id,
					"attempt", attempt,
					"max_attempts", config.MaxRetryAttempts,
					"retry_delay", retryDelay,
					"error", shutdownErr)

				if attempt < config.MaxRetryAttempts {
					time.Sleep(retryDelay)
					// Exponential backoff with max delay cap
					retryDelay = retryDelay * config.RetryBackoffMultiple
					if retryDelay > config.MaxRetryDelay {
						retryDelay = config.MaxRetryDelay
					}
					// Refresh server state before retry
					server, _, err = s.connector.client.Server.GetByID(ctx, s.id)
					if err != nil {
						return fmt.Errorf("refresh server state: %w", err)
					}
					if server == nil {
						return fmt.Errorf("server with ID %d not found during retry", s.id)
					}
					continue
				}
			}

			return fmt.Errorf("shutdown server: %w", shutdownErr)
		}

		// Wait for server to stop
		s.log.Info("waiting for server to stop", "server_id", s.id)
		if err := s.waitForStatus(ctx, hcloud.ServerStatusOff, 2*time.Minute); err != nil {
			return err
		}
		s.log.Info("server stopped", "server_id", s.id)
	} else {
		s.log.Info("server already stopped", "server_id", s.id, "status", server.Status)
	}

	// Delete the server with retry logic
	s.log.Info("deleting server from hetzner cloud", "server_id", s.id)
	retryDelay := config.InitialRetryDelay
	var deleteErr error
	for attempt := 1; attempt <= config.MaxRetryAttempts; attempt++ {
		_, _, deleteErr = s.connector.client.Server.DeleteWithResult(ctx, server)
		if deleteErr == nil {
			break
		}

		if isResourceLockedError(deleteErr) {
			s.log.Warn("server is locked, retrying delete",
				"server_id", s.id,
				"attempt", attempt,
				"max_attempts", config.MaxRetryAttempts,
				"retry_delay", retryDelay,
				"error", deleteErr)

			if attempt < config.MaxRetryAttempts {
				time.Sleep(retryDelay)
				// Exponential backoff with max delay cap
				retryDelay = retryDelay * config.RetryBackoffMultiple
				if retryDelay > config.MaxRetryDelay {
					retryDelay = config.MaxRetryDelay
				}
				// Refresh server state before retry
				server, _, err = s.connector.client.Server.GetByID(ctx, s.id)
				if err != nil {
					return fmt.Errorf("refresh server state: %w", err)
				}
				if server == nil {
					return fmt.Errorf("server with ID %d not found during retry", s.id)
				}
				continue
			}
		}

		return fmt.Errorf("delete server: %w", deleteErr)
	}

	s.log.Info("server deleted successfully", "server_id", s.id, "server_name", s.name)
	return nil
}

// waitForStatus waits for the server to reach the expected status
func (s *Server) waitForStatus(ctx context.Context, expectedStatus hcloud.ServerStatus, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		state, err := s.GetState()
		if err != nil {
			return fmt.Errorf("get server state: %w", err)
		}
		if state == string(expectedStatus) {
			return nil
		}
		<-ticker.C
	}
	return fmt.Errorf("timeout waiting for server to reach status %s", expectedStatus)
}

func (s *Server) String() string {
	return fmt.Sprintf("%v [%v]", s.name, s.ipv6)
}

// parseServerID converts string ID to int64
func parseServerID(id string) (int64, error) {
	idInt, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid server ID: %w", err)
	}
	return idInt, nil
}

var _ connector.Server = (*Server)(nil)
