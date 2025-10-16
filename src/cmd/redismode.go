package main

import (
	"fmt"
	"log/slog"
	"time"
	"os"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

const (
	provisionQueueKey = "swim:provision:queue"
	serverCachePrefix = "swim:server:"
	serverCacheTTL    = 24 * time.Hour
	queueTimeout      = 30 * time.Second
)

// runRedisMode handles service mode with Redis queue
func runRedisMode(log *slog.Logger, conn connector.Connector, connectionString string) {
	log.Info("Running in Redis mode", "connection", connectionString)

	// Create Redis client
	redisClient, err := redis.NewClient(redis.Config{
		Address:  connectionString,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
	if err != nil {
		log.Error("Failed to connect to Redis", "error", err)
		return
	}
	defer redisClient.Close()

	log.Info("Connected to Redis, waiting for provision requests...")

	// Main loop: continuously process requests from the queue
	for {
		// Pop payload from Redis queue (blocking)
		payload, err := redisClient.PopPayload(provisionQueueKey, queueTimeout)
		if err != nil {
			log.Debug("Failed to pop payload from queue", "error", err)
			continue
		}

		log.Info("Received provision request", "payload_length", len(payload))

		// Create server using the connector
		server, err := conn.CreateServer(payload)
		if err != nil {
			log.Error("Failed to provision server", "error", err)
			continue
		}

		log.Info("Server provisioned successfully", "id", server.GetID(), "name", server.GetName())

		// Get server state
		state, err := server.GetState()
		if err != nil {
			log.Warn("Failed to get server state", "error", err)
			state = "unknown"
		}

		// Push server state into Redis cache
		serverState := redis.ServerState{
			ID:            server.GetID(),
			Name:          server.GetName(),
			IPv6:          server.GetIPv6Address(),
			State:         state,
			ProvisionedAt: time.Now(),
		}

		cacheKey := fmt.Sprintf("%s%s", serverCachePrefix, server.GetID())
		if err := redisClient.PushServerState(cacheKey, serverState, serverCacheTTL); err != nil {
			log.Error("Failed to cache server state", "error", err)
		} else {
			log.Info("Server state cached", "cache_key", cacheKey)
		}

		fmt.Printf("Provisioned server: %+v\n", server)
	}
}
