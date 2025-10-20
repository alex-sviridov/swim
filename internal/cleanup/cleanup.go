package cleanup

import (
	"context"
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

// cleanupExpiredServers finds and deletes expired servers
func (w *Worker) cleanupExpiredServers(ctx context.Context) {
	expired, err := w.redisClient.GetExpiredServers(ctx, config.ServerCachePrefix)
	if err != nil {
		w.log.Error("failed to get expired servers", "error", err)
		return
	}

	if len(expired) == 0 {
		return
	}

	w.log.Info("found expired servers", "count", len(expired))

	for _, state := range expired {
		// Check if context was cancelled
		select {
		case <-ctx.Done():
			w.log.Info("cleanup interrupted, stopping")
			return
		default:
		}

		go w.deleteExpiredServer(state)
	}
}

// deleteExpiredServer deletes a single expired server
func (w *Worker) deleteExpiredServer(state redis.ServerState) {
	ctx := context.Background()
	cacheKey := redis.ServerCacheKey(state.ID)

	// Update state to "shutting down"
	state.State = "shutting down"
	if err := w.redisClient.PushServerState(ctx, cacheKey, state, config.ServerCacheTTL); err != nil {
		w.log.Error("failed to update server state to shutting down", "server_id", state.ID, "error", err)
	}

	server, err := w.conn.GetServerByID(state.ID)
	if err != nil {
		w.log.Error("failed to get expired server", "server_id", state.ID, "error", err)
		// Remove from cache if server not found (already deleted)
		if err := w.redisClient.DeleteServerState(ctx, cacheKey); err != nil {
			w.log.Error("failed to remove non-existent server from cache", "server_id", state.ID, "error", err)
		} else {
			w.log.Info("removed non-existent server from cache", "server_id", state.ID)
		}
		return
	}

	if err := server.Delete(); err != nil {
		w.log.Error("failed to delete expired server", "server_id", state.ID, "error", err)
		return
	}

	// Remove from Redis cache after successful deletion
	if err := w.redisClient.DeleteServerState(ctx, cacheKey); err != nil {
		w.log.Error("failed to remove server from cache", "server_id", state.ID, "error", err)
	}

	w.log.Info("deleted expired server and removed from cache", "server_id", state.ID)
}
