package decommissioner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

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
	WebUserID string `json:"webuserid"`
	LabID     *int   `json:"labId,omitempty"`    // Optional: if provided, validates against cached labId to prevent stale requests
	ServerID  string `json:"serverId,omitempty"` // Optional: if provided, allows deletion even when cache entry is missing
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
	if req.WebUserID == "" {
		d.log.Error("webuserid is required in decommission request")
		return
	}

	if req.LabID != nil {
		d.log.Info("processing decommission request with labId validation", "webuserid", req.WebUserID, "labid", *req.LabID)
	} else {
		d.log.Info("processing decommission request without labId", "webuserid", req.WebUserID)
	}

	// Check rate limit with retry logic
	rateLimitTTL := config.GetDecommissionRateLimitDuration()
	allowed, err := d.tryAcquireRateLimitWithRetry(ctx, req.WebUserID, "decommission", rateLimitTTL)
	if err != nil {
		d.log.Error("failed to check rate limit after retries, dropping message", "webuserid", req.WebUserID, "error", err)
		return
	}
	if !allowed {
		if req.LabID != nil {
			d.log.Warn("decommission rate limit hit, dropping message", "webuserid", req.WebUserID, "labid", *req.LabID)
		} else {
			d.log.Warn("decommission rate limit hit, dropping message", "webuserid", req.WebUserID)
		}
		return
	}

	// Build cache key (note: labId is stored in the state, not the key)
	cacheKey := redis.ServerCacheKey(req.WebUserID)

	// Get server state from cache
	serverState, err := d.redisClient.GetServerState(ctx, cacheKey)
	if err != nil {
		// Cache miss - check if we have serverID in the request payload
		if req.ServerID != "" {
			d.log.Info("server not found in cache but serverID provided in request, proceeding with deletion",
				"webuserid", req.WebUserID,
				"server_id", req.ServerID)
			// Delete directly using serverID from request
			d.deleteServerByID(ctx, req.ServerID)
			d.log.Info("decommission request completed (cache-less deletion)", "webuserid", req.WebUserID, "server_id", req.ServerID)
			return
		}
		d.log.Warn("server not found in cache and no serverID provided, cannot proceed", "webuserid", req.WebUserID, "error", err)
		return
	}

	// If labId is provided, verify it matches to prevent stale decommission requests
	if req.LabID != nil && serverState.LabID != *req.LabID {
		// LabID mismatch - this means cache was replaced by new provision
		// If we have serverID in request, use cache-less deletion for the old server
		if req.ServerID != "" {
			d.log.Info("labId mismatch but serverID provided, using cache-less deletion for old server",
				"webuserid", req.WebUserID,
				"requested_labid", *req.LabID,
				"current_labid", serverState.LabID,
				"server_id", req.ServerID)
			d.deleteServerByID(ctx, req.ServerID)
			d.log.Info("decommission request completed (cache-less deletion due to labId mismatch)", "webuserid", req.WebUserID, "server_id", req.ServerID)
			return
		}
		d.log.Warn("labId mismatch, ignoring stale decommission request",
			"webuserid", req.WebUserID,
			"requested_labid", *req.LabID,
			"current_labid", serverState.LabID)
		return
	}

	// Delete the server
	d.deleteServer(ctx, cacheKey, *serverState)

	if req.LabID != nil {
		d.log.Info("decommission request completed", "webuserid", req.WebUserID, "labid", *req.LabID)
	} else {
		d.log.Info("decommission request completed", "webuserid", req.WebUserID, "labid", serverState.LabID)
	}
}

// deleteServer deletes a single server and removes from cache
func (d *Decommissioner) deleteServer(ctx context.Context, cacheKey string, serverState redis.ServerState) {
	serverLog := d.log.With("server_id", serverState.ServerID, "address", serverState.Address)

	// Update status to "stopping"
	serverState.Status = config.StatusStopping
	serverState.Available = false
	serverState.CloudStatus = "stopping"
	if err := d.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); err != nil {
		serverLog.Error("failed to update server status to stopping", "error", err)
	}

	// Get server from connector using the ServerID
	server, err := d.conn.GetServerByID(serverState.ServerID)
	if err != nil {
		serverLog.Warn("failed to get server for decommissioning (may already be deleted)", "error", err)
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
		return
	}

	// Remove from Redis cache after successful deletion
	if err := d.redisClient.DeleteServerState(ctx, cacheKey); err != nil {
		serverLog.Error("failed to remove server from cache after deletion", "error", err)
	} else {
		serverLog.Info("server decommissioned and removed from cache")
	}
}

// deleteServerByID deletes a server by its ID without using cache
// This is used when cache entry is missing but we have serverID from the decommission request
func (d *Decommissioner) deleteServerByID(ctx context.Context, serverID string) {
	serverLog := d.log.With("server_id", serverID)

	// Get server from connector using the ServerID
	server, err := d.conn.GetServerByID(serverID)
	if err != nil {
		serverLog.Warn("failed to get server for decommissioning (may already be deleted)", "error", err)
		return
	}

	// Delete the server
	if err := server.Delete(); err != nil {
		serverLog.Error("failed to delete server", "error", err)
		return
	}

	serverLog.Info("server decommissioned successfully (cache-less deletion)")
}

// tryAcquireRateLimitWithRetry attempts to acquire rate limit with retry logic
// Returns (true, nil) if rate limit acquired successfully
// Returns (false, nil) if rate limited (another request within TTL window)
// Returns (false, error) if all retries exhausted with Redis errors
func (d *Decommissioner) tryAcquireRateLimitWithRetry(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	var lastErr error

	for attempt := 1; attempt <= config.CacheReadRetryAttempts; attempt++ {
		allowed, err := d.redisClient.TryAcquireRateLimit(ctx, webUserID, operation, ttl)
		if err == nil {
			// Success - return whether rate limit was acquired
			return allowed, nil
		}

		// It's a real error (Redis connection issue, etc.)
		lastErr = err
		d.log.Warn("failed to check rate limit, retrying",
			"attempt", attempt,
			"max_attempts", config.CacheReadRetryAttempts,
			"error", err)

		// Don't sleep after the last attempt
		if attempt < config.CacheReadRetryAttempts {
			time.Sleep(config.CacheReadRetryTimeout)
		}
	}

	// All retries exhausted
	return false, fmt.Errorf("failed to check rate limit after %d attempts: %w", config.CacheReadRetryAttempts, lastErr)
}
