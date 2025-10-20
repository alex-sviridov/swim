package decommissioner

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

// Decommissioner handles server decommissioning workflows
type Decommissioner struct {
	log         *slog.Logger
	conn        connector.Connector
	redisClient redis.ClientInterface
}

// New creates a new Decommissioner
func New(log *slog.Logger, conn connector.Connector, redisClient redis.ClientInterface) *Decommissioner {
	return &Decommissioner{
		log:         log,
		conn:        conn,
		redisClient: redisClient,
	}
}

// DecommissionRequest represents a decommission request payload
type DecommissionRequest struct {
	WebUsername string `json:"WebUsername"`
	LabID       *int   `json:"LabID,omitempty"`
}

// ProcessRequest handles a single decommission request from the queue
func (d *Decommissioner) ProcessRequest(ctx context.Context, payload string) {
	// Parse the decommission request
	var req DecommissionRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		d.log.Error("failed to parse decommission payload", "error", err)
		return
	}

	// Validate required fields
	if req.WebUsername == "" {
		d.log.Error("WebUsername is required in decommission request")
		return
	}

	// Log decommission request details
	if req.LabID != nil {
		d.log.Info("processing decommission request", "username", req.WebUsername, "lab_id", *req.LabID)
	} else {
		d.log.Info("processing decommission request for all user VMs", "username", req.WebUsername)
	}

	// Get servers matching the filter criteria
	servers, err := d.redisClient.GetServersByFilter(ctx, config.ServerCachePrefix, req.WebUsername, req.LabID)
	if err != nil {
		d.log.Error("failed to get servers by filter", "error", err, "username", req.WebUsername)
		return
	}

	if len(servers) == 0 {
		d.log.Info("no servers found matching decommission criteria", "username", req.WebUsername)
		return
	}

	d.log.Info("found servers to decommission", "count", len(servers), "username", req.WebUsername)

	// Delete each server
	for _, serverState := range servers {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			d.log.Info("decommission interrupted, stopping")
			return
		default:
		}

		d.deleteServer(ctx, serverState)
	}

	d.log.Info("decommission request completed", "username", req.WebUsername, "servers_processed", len(servers))
}

// deleteServer deletes a single server and updates cache
func (d *Decommissioner) deleteServer(ctx context.Context, serverState redis.ServerState) {
	serverLog := d.log.With("server_id", serverState.ID, "server_name", serverState.Name, "username", serverState.WebUsername, "lab_id", serverState.LabID)
	cacheKey := redis.ServerCacheKey(serverState.ID)

	// Update state to "decommissioning"
	serverState.State = "decommissioning"
	if err := d.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); err != nil {
		serverLog.Error("failed to update server state to decommissioning", "error", err)
	}

	// Get server from connector
	server, err := d.conn.GetServerByID(serverState.ID)
	if err != nil {
		serverLog.Error("failed to get server for decommissioning", "error", err)
		// Remove from cache if server not found (already deleted)
		if err := d.redisClient.DeleteServerState(ctx, cacheKey); err != nil {
			serverLog.Error("failed to remove non-existent server from cache", "error", err)
		} else {
			serverLog.Info("removed non-existent server from cache")
		}
		return
	}

	// Delete the server
	if err := server.Delete(); err != nil {
		serverLog.Error("failed to delete server", "error", err)
		// Update cache with error state
		serverState.State = config.StateDeletedError
		if cacheErr := d.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); cacheErr != nil {
			serverLog.Error("failed to cache deleted-error state", "error", cacheErr)
		}
		return
	}

	// Remove from Redis cache after successful deletion
	if err := d.redisClient.DeleteServerState(ctx, cacheKey); err != nil {
		serverLog.Error("failed to remove server from cache after deletion", "error", err)
	} else {
		serverLog.Info("server decommissioned and removed from cache")
	}
}
