package provisioner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/alex-sviridov/swim/internal/config"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

const (
	defaultPollInterval = 15 * time.Second
	stateTimeout        = 300 * time.Second
)

// Provisioner handles server provisioning workflows
type Provisioner struct {
	log          *slog.Logger
	conn         connector.Connector
	redisClient  redis.ClientInterface
	pollInterval time.Duration
}

// New creates a new Provisioner
func New(log *slog.Logger, conn connector.Connector, redisClient redis.ClientInterface) *Provisioner {
	return &Provisioner{
		log:          log,
		conn:         conn,
		redisClient:  redisClient,
		pollInterval: defaultPollInterval,
	}
}

// WithPollInterval sets a custom poll interval (useful for testing)
func (p *Provisioner) WithPollInterval(interval time.Duration) *Provisioner {
	p.pollInterval = interval
	return p
}

// ProcessRequest handles a single provision request from the queue
func (p *Provisioner) ProcessRequest(ctx context.Context, payload string) {
	// Extract WebUserID and LabID from the minimal request
	var req struct {
		WebUserID string `json:"webuserid"`
		LabID     int    `json:"labId"`
	}
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		p.log.Error("failed to parse payload", "error", err)
		return
	}

	serverLog := p.log.With("webuserid", req.WebUserID, "labid", req.LabID)

	// Check rate limit with retry logic
	rateLimitTTL := config.GetProvisionRateLimitDuration()
	allowed, err := p.tryAcquireRateLimitWithRetry(ctx, req.WebUserID, "provision", rateLimitTTL)
	if err != nil {
		serverLog.Error("failed to check rate limit after retries, dropping message", "error", err)
		return
	}
	if !allowed {
		serverLog.Warn("provision rate limit hit, dropping message")
		return
	}

	// Build cache key (note: labId is stored in the state, not the key)
	cacheKey := redis.ServerCacheKey(req.WebUserID)

	// Check if server already exists in cache with retry logic
	existingState, err := p.getServerStateWithRetry(ctx, cacheKey)
	if err != nil {
		serverLog.Error("failed to check existing server state after retries, aborting provision", "error", err)
		return
	}

	if existingState != nil {
		// Server exists in cache
		if existingState.LabID == req.LabID {
			// Same labId - this is a duplicate request, do nothing
			serverLog.Info("server already exists with same labId, ignoring duplicate request",
				"server_id", existingState.ServerID,
				"status", existingState.Status,
				"address", existingState.Address)
			return
		}

		// Different labId - need to decommission old server and provision new one
		serverLog.Info("server exists with different labId, triggering decommission and starting new provision",
			"old_labid", existingState.LabID,
			"new_labid", req.LabID,
			"old_server_id", existingState.ServerID)

		// Push decommission request to queue (non-blocking)
		// Include serverID so decommissioner can delete even if cache entry is replaced
		decommissionPayload := fmt.Sprintf(`{"webuserid":"%s","labId":%d,"serverId":"%s"}`,
			req.WebUserID, existingState.LabID, existingState.ServerID)
		if err := p.redisClient.PushPayload(ctx, config.DecommissionQueueKey, decommissionPayload); err != nil {
			serverLog.Error("failed to queue decommission request", "error", err)
			// Continue with provisioning anyway - decommission can be handled later
		} else {
			serverLog.Info("decommission request queued for old server", "old_server_id", existingState.ServerID)
		}
		// Continue with provisioning new server below
	}

	// Get SSH username from environment (default: "student")
	sshUsername := "student"
	if envUser := os.Getenv("SSH_USERNAME"); envUser != "" {
		sshUsername = envUser
	}

	// Get TTL from environment (default: 30 minutes)
	ttlMinutes := 30
	if envTTL := os.Getenv("DEFAULT_TTL_MINUTES"); envTTL != "" {
		if ttl, err := strconv.Atoi(envTTL); err == nil {
			ttlMinutes = ttl
		}
	}
	expiresAt := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)

	// Set initial provisioning state in cache
	initialState := redis.ServerState{
		User:        sshUsername,
		Address:     "", // Will be set after provisioning
		Status:      config.StatusProvisioning,
		Available:   false, // Not available until running
		CloudStatus: "",    // Will be set after provisioning
		ServerID:    "",    // Will be set after provisioning
		ExpiresAt:   expiresAt,
		WebUserID:   req.WebUserID,
		LabID:       req.LabID,
	}

	if err := p.redisClient.PushServerState(ctx, cacheKey, initialState, config.ServerCacheTTL); err != nil {
		serverLog.Error("failed to cache initial provisioning state", "error", err)
		// Continue anyway - caching failure shouldn't stop provisioning
	} else {
		serverLog.Info("initial provisioning state cached")
	}

	// Create server using the connector (validation happens inside)
	server, err := p.conn.CreateServer(payload)
	if err != nil {
		serverLog.Error("failed to provision server", "error", err)
		// Delete cache on error
		p.redisClient.DeleteServerState(ctx, cacheKey)
		return
	}

	serverLog = serverLog.With("server_id", server.GetID(), "server_name", server.GetName())
	serverLog.Info("server provisioned successfully")

	// Get initial server state from cloud provider
	cloudState, err := server.GetState()
	if err != nil {
		serverLog.Warn("failed to get server state", "error", err)
		cloudState = "unknown"
	}

	// Update cache with server details
	serverState := redis.ServerState{
		User:        sshUsername,
		Address:     server.GetIPv6Address(),
		Status:      mapCloudStateToStatus(cloudState),
		Available:   isServerAvailable(cloudState),
		CloudStatus: cloudState,
		ServerID:    server.GetID(),
		ExpiresAt:   expiresAt,
		WebUserID:   req.WebUserID,
		LabID:       req.LabID,
	}

	if err := p.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); err != nil {
		serverLog.Error("failed to cache server state", "error", err)
	} else {
		serverLog.Info("server state cached", "status", serverState.Status, "address", serverState.Address)
	}

	serverLog.Info("provisioned server details", "server", server.String())

	// Poll for state changes
	p.pollServerState(ctx, server, cacheKey, serverState, cloudState)
}

// pollServerState polls for server state changes until running or timeout
func (p *Provisioner) pollServerState(ctx context.Context, server connector.Server, cacheKey string, serverState redis.ServerState, initialState string) {
	serverLog := p.log.With("server_id", server.GetID())

	ticker := time.NewTicker(p.pollInterval)
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

				serverState.Status = mapCloudStateToStatus(currentState)
				serverState.Available = isServerAvailable(currentState)
				serverState.CloudStatus = currentState
				if err := p.redisClient.PushServerState(ctx, cacheKey, serverState, config.ServerCacheTTL); err != nil {
					p.handleProvisioningError(ctx, server, cacheKey, serverState, "failed to update server state in cache", err)
					return
				}
				serverLog.Info("server state updated in cache", "status", serverState.Status, "available", serverState.Available, "cloud_status", serverState.CloudStatus)

				lastState = currentState
			}

			// Exit if server is running
			if currentState == "running" {
				serverLog.Info("server is running, stopping state polling")
				return
			}
		}
	}
}

// mapCloudStateToStatus maps cloud provider state to VMManager status
func mapCloudStateToStatus(cloudState string) string {
	switch cloudState {
	case "running":
		return config.StatusRunning
	case "starting", "initializing":
		return config.StatusProvisioning
	case "stopping", "off", "deleting":
		return config.StatusStopping
	default:
		return config.StatusProvisioning
	}
}

// isServerAvailable determines if server is ready for SSH connections
// This logic can vary by cloud provider - for Hetzner, only "running" means available
func isServerAvailable(cloudState string) bool {
	return cloudState == "running"
}

// handleProvisioningError deletes the server and removes from cache
func (p *Provisioner) handleProvisioningError(ctx context.Context, server connector.Server, cacheKey string, serverState redis.ServerState, errorMsg string, err error) {
	serverLog := p.log.With("server_id", server.GetID())
	serverLog.Error(errorMsg, "error", err)

	// Delete the server
	if delErr := server.Delete(); delErr != nil {
		serverLog.Error("failed to delete server after error", "error", delErr)
	} else {
		serverLog.Info("server deleted due to error")
	}

	// Remove from cache
	if cacheErr := p.redisClient.DeleteServerState(ctx, cacheKey); cacheErr != nil {
		serverLog.Error("failed to delete cache after error", "error", cacheErr)
	} else {
		serverLog.Info("removed server from cache after error")
	}
}

// getServerStateWithRetry attempts to get server state from cache with retry logic
// Returns (nil, nil) if server not found in cache
// Returns (nil, error) if all retries exhausted with errors
// Returns (state, nil) if server found successfully
func (p *Provisioner) getServerStateWithRetry(ctx context.Context, cacheKey string) (*redis.ServerState, error) {
	var lastErr error

	for attempt := 1; attempt <= config.CacheReadRetryAttempts; attempt++ {
		state, err := p.redisClient.GetServerState(ctx, cacheKey)
		if err == nil {
			// Success - server found
			return state, nil
		}

		// Check if it's a "not found" error (which is not a failure, just means no server exists)
		if err.Error() == "server state not found in cache" {
			// Server doesn't exist in cache - this is a normal case, not an error
			return nil, nil
		}

		// It's a real error (Redis connection issue, etc.)
		lastErr = err
		p.log.Warn("failed to read server state from cache, retrying",
			"attempt", attempt,
			"max_attempts", config.CacheReadRetryAttempts,
			"error", err)

		// Don't sleep after the last attempt
		if attempt < config.CacheReadRetryAttempts {
			time.Sleep(config.CacheReadRetryTimeout)
		}
	}

	// All retries exhausted
	return nil, fmt.Errorf("failed to read from cache after %d attempts: %w", config.CacheReadRetryAttempts, lastErr)
}

// tryAcquireRateLimitWithRetry attempts to acquire rate limit with retry logic
// Returns (true, nil) if rate limit acquired successfully
// Returns (false, nil) if rate limited (another request within TTL window)
// Returns (false, error) if all retries exhausted with Redis errors
func (p *Provisioner) tryAcquireRateLimitWithRetry(ctx context.Context, webUserID string, operation string, ttl time.Duration) (bool, error) {
	var lastErr error

	for attempt := 1; attempt <= config.CacheReadRetryAttempts; attempt++ {
		allowed, err := p.redisClient.TryAcquireRateLimit(ctx, webUserID, operation, ttl)
		if err == nil {
			// Success - return whether rate limit was acquired
			return allowed, nil
		}

		// It's a real error (Redis connection issue, etc.)
		lastErr = err
		p.log.Warn("failed to check rate limit, retrying",
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
