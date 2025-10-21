package provisioner

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

const (
	statePollInterval = 15 * time.Second
	stateTimeout      = 300 * time.Second
)

// Provisioner handles server provisioning workflows
type Provisioner struct {
	log         *slog.Logger
	conn        connector.Connector
	redisClient redis.ClientInterface
}

// New creates a new Provisioner
func New(log *slog.Logger, conn connector.Connector, redisClient redis.ClientInterface) *Provisioner {
	return &Provisioner{
		log:         log,
		conn:        conn,
		redisClient: redisClient,
	}
}

// ProcessRequest handles a single provision request from the queue
func (p *Provisioner) ProcessRequest(ctx context.Context, payload string) {
	// Extract TTL, WebUsername, and LabID for deletion time calculation and caching
	var req struct {
		TTLMinutes  int    `json:"TTLMinutes"`
		WebUsername string `json:"WebUsername"`
		LabID       int    `json:"LabID"`
	}
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		p.log.Error("failed to parse payload", "error", err)
		return
	}
	deletionAt := time.Now().Add(time.Duration(req.TTLMinutes) * time.Minute)

	// Create server using the connector (validation happens inside)
	server, err := p.conn.CreateServer(payload)
	if err != nil {
		p.log.Error("failed to provision server", "error", err)
		// Server creation failed - CreateServer already handles cleanup on error
		return
	}

	serverLog := p.log.With("server_id", server.GetID(), "server_name", server.GetName())
	serverLog.Info("server provisioned successfully")

	// Get initial server state
	state, err := server.GetState()
	if err != nil {
		serverLog.Warn("failed to get server state", "error", err)
		state = "unknown"
	}

	// Push initial server state into Redis cache
	now := time.Now()
	cacheKey := redis.ServerCacheKey(server.GetID())
	serverState := redis.ServerState{
		ID:            server.GetID(),
		Name:          server.GetName(),
		IPv6:          server.GetIPv6Address(),
		State:         state,
		ProvisionedAt: now,
		DeletionAt:    deletionAt,
		WebUsername:   req.WebUsername,
		LabID:         req.LabID,
	}

	if err := p.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); err != nil {
		serverLog.Error("failed to cache server state", "error", err)
		// Continue anyway - caching failure shouldn't stop provisioning
	} else {
		serverLog.Info("server state cached", "cache_key", cacheKey, "state", state, "deletion_at", deletionAt)
	}

	serverLog.Info("provisioned server details", "server", server.String())

	// Poll for state changes
	p.pollServerState(ctx, server, cacheKey, serverState, state)
}

// pollServerState polls for server state changes until running or timeout
func (p *Provisioner) pollServerState(ctx context.Context, server connector.Server, cacheKey string, serverState redis.ServerState, initialState string) {
	serverLog := p.log.With("server_id", server.GetID())

	ticker := time.NewTicker(statePollInterval)
	defer ticker.Stop()

	timeout := time.After(stateTimeout)
	lastState := initialState

	for {
		select {
		case <-ctx.Done():
			serverLog.Info("context cancelled, stopping state polling")
			return

		case <-timeout:
			serverLog.Info("state polling timeout reached", "final_state", lastState)
			return

		case <-ticker.C:
			currentState, err := server.GetState()
			if err != nil {
				p.handleProvisioningError(ctx, server, cacheKey, serverState, "failed to get server state during polling", err)
				return
			}

			// Update cache if state changed
			if currentState != lastState {
				serverLog.Info("server state changed", "old_state", lastState, "new_state", currentState)

				serverState.State = currentState
				if err := p.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); err != nil {
					p.handleProvisioningError(ctx, server, cacheKey, serverState, "failed to update server state in cache", err)
					return
				}
				serverLog.Info("server state updated in cache", "state", currentState)

				lastState = currentState
			}

			// Exit if server is running
			if currentState == config.StateRunning {
				serverLog.Info("server is running, stopping state polling")
				return
			}
		}
	}
}

// handleProvisioningError deletes the server and caches error state
func (p *Provisioner) handleProvisioningError(ctx context.Context, server connector.Server, cacheKey string, serverState redis.ServerState, errorMsg string, err error) {
	serverLog := p.log.With("server_id", server.GetID())
	serverLog.Error(errorMsg, "error", err)

	// Delete the server
	if delErr := server.Delete(); delErr != nil {
		serverLog.Error("failed to delete server after error", "error", delErr)
		// Even if deletion fails, update cache to reflect error state
		serverState.State = config.StateDeletedError
		if cacheErr := p.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); cacheErr != nil {
			serverLog.Error("failed to cache error state after deletion failure", "error", cacheErr)
		}
	} else {
		serverLog.Info("server deleted due to error")
		// Update cache with deleted-error state
		serverState.State = config.StateDeletedError
		if cacheErr := p.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); cacheErr != nil {
			serverLog.Error("failed to cache deleted-error state", "error", cacheErr)
		} else {
			serverLog.Info("cached deleted-error state")
		}
	}
}
