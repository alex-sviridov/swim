package cleanup

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
	cleanupInterval = 5 * time.Minute
)

// Worker handles periodic cleanup of expired servers
type Worker struct {
	log         *slog.Logger
	conn        connector.Connector
	redisClient redis.ClientInterface
}

// New creates a new cleanup Worker
func New(log *slog.Logger, conn connector.Connector, redisClient redis.ClientInterface) *Worker {
	return &Worker{
		log:         log,
		conn:        conn,
		redisClient: redisClient,
	}
}

// Run starts the cleanup worker, running until context is cancelled
func (w *Worker) Run(ctx context.Context) {
	w.log.Info("cleanup worker started")

	// Run cleanup immediately on startup
	w.cleanupExpiredServers(ctx)

	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.log.Info("cleanup worker stopping")
			return
		case <-ticker.C:
			w.cleanupExpiredServers(ctx)
		}
	}
}

// cleanupExpiredServers finds expired servers and pushes decommission requests to queue
func (w *Worker) cleanupExpiredServers(ctx context.Context) {
	// Get all server states
	servers, err := w.redisClient.GetAllServerStates(ctx, config.ServerCachePrefix)
	if err != nil {
		w.log.Error("failed to get server states", "error", err)
		return
	}

	if len(servers) == 0 {
		return
	}

	now := time.Now()
	expiredCount := 0

	for _, state := range servers {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			w.log.Info("cleanup interrupted, stopping")
			return
		default:
		}

		// Check if server is expired
		if state.ExpiresAt.Before(now) {
			expiredCount++
			w.pushDecommissionRequest(ctx, state)
		}
	}

	if expiredCount > 0 {
		w.log.Info("found expired servers, pushed decommission requests", "count", expiredCount)
	}
}

// pushDecommissionRequest pushes a decommission request to the queue for an expired server
func (w *Worker) pushDecommissionRequest(ctx context.Context, state redis.ServerState) {
	// Create decommission request payload
	decomReq := map[string]interface{}{
		"webuserid": state.WebUserID,
		"labId":     state.LabID,
	}

	payload, err := json.Marshal(decomReq)
	if err != nil {
		w.log.Error("failed to marshal decommission request", "error", err)
		return
	}

	// Push to decommission queue
	if err := w.redisClient.PushPayload(ctx, config.DecommissionQueueKey, string(payload)); err != nil {
		w.log.Error("failed to push decommission request", "error", err)
		return
	}

	w.log.Info("pushed decommission request for expired server",
		"server_id", state.ServerID,
		"webuserid", state.WebUserID,
		"labid", state.LabID)
}
