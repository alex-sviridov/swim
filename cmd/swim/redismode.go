package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/redis"
)

const (
	provisionQueueKey = "swim:provision:queue"
	serverCachePrefix = "swim:server:"
	serverCacheTTL    = 24 * time.Hour
	queueTimeout      = 30 * time.Second
	statePollInterval = 15 * time.Second
	stateTimeout      = 300 * time.Second
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

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup

	// Goroutine to handle shutdown signals
	go func() {
		<-sigChan
		log.Info("Shutdown signal received, stopping gracefully...")
		cancel()
	}()

	// Main loop: continuously process requests from the queue
	for {
		// Check if shutdown was requested
		select {
		case <-ctx.Done():
			log.Info("Waiting for active provisioning tasks to complete...")
			wg.Wait()
			log.Info("All tasks completed, shutting down")
			return
		default:
		}

		// Pop payload from Redis queue (blocking)
		payload, err := redisClient.PopPayload(provisionQueueKey, queueTimeout)
		if err != nil {
			log.Debug("Failed to pop payload from queue", "error", err)
			continue
		}

		log.Info("Received provision request", "payload_length", len(payload))

		// Provision server in a goroutine
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()

			// Parse payload to extract TTL and server name for error handling
			var requestData struct {
				ServerName string `json:"ServerName"`
				TTLMinutes int    `json:"TTLMinutes"`
			}
			if err := json.Unmarshal([]byte(payload), &requestData); err != nil {
				log.Error("Failed to parse payload", "error", err)
				return
			}

			now := time.Now()
			deletionAt := now.Add(time.Duration(requestData.TTLMinutes) * time.Minute)

			// Create server using the connector
			server, err := conn.CreateServer(payload)
			if err != nil {
				log.Error("Failed to provision server", "error", err, "server_name", requestData.ServerName)

				// Server creation failed - if we got a partial server back, delete it and cache error state
				// In this implementation, CreateServer already handles cleanup on error
				// We just need to log the failure - no server to cache
				return
			}

			log.Info("Server provisioned successfully", "id", server.GetID(), "name", server.GetName())

			// Get initial server state
			state, err := server.GetState()
			if err != nil {
				log.Warn("Failed to get server state", "error", err)
				state = "unknown"
			}

			// Push initial server state into Redis cache
			cacheKey := fmt.Sprintf("%s%s", serverCachePrefix, server.GetID())
			serverState := redis.ServerState{
				ID:            server.GetID(),
				Name:          server.GetName(),
				IPv6:          server.GetIPv6Address(),
				State:         state,
				ProvisionedAt: now,
				DeletionAt:    deletionAt,
			}

			if err := redisClient.PushServerState(cacheKey, serverState, serverCacheTTL); err != nil {
				log.Error("Failed to cache server state", "error", err)
			} else {
				log.Info("Server state cached", "cache_key", cacheKey, "state", state, "deletion_at", deletionAt)
			}

			log.Info("Provisioned server details", "server", server.String())

			// Helper function to delete server and set error state in cache
			handleError := func(errorMsg string, err error) {
				log.Error(errorMsg, "server_id", server.GetID(), "error", err)

				// Delete the server
				if delErr := server.Delete(); delErr != nil {
					log.Error("Failed to delete server after error", "server_id", server.GetID(), "error", delErr)
				} else {
					log.Info("Server deleted due to error", "server_id", server.GetID())
				}

				// Update cache with deleted-error state
				serverState.State = "deleted-error"
				if cacheErr := redisClient.PushServerState(cacheKey, serverState, serverCacheTTL); cacheErr != nil {
					log.Error("Failed to cache deleted-error state", "error", cacheErr)
				} else {
					log.Info("Cached deleted-error state", "server_id", server.GetID())
				}
			}

			// Poll for state changes
			ticker := time.NewTicker(statePollInterval)
			defer ticker.Stop()

			timeout := time.After(stateTimeout)
			lastState := state

			for {
				select {
				case <-timeout:
					log.Info("State polling timeout reached", "server_id", server.GetID(), "final_state", lastState)
					return

				case <-ticker.C:
					currentState, err := server.GetState()
					if err != nil {
						handleError("Failed to get server state during polling", err)
						return
					}

					// Update cache if state changed
					if currentState != lastState {
						log.Info("Server state changed", "server_id", server.GetID(), "old_state", lastState, "new_state", currentState)
						lastState = currentState

						serverState.State = currentState
						if err := redisClient.PushServerState(cacheKey, serverState, serverCacheTTL); err != nil {
							handleError("Failed to update server state in cache", err)
							return
						}
						log.Info("Server state updated in cache", "server_id", server.GetID(), "state", currentState)
					}

					// Exit if server is running
					if currentState == "booted" {
						log.Info("Server is running, stopping state polling", "server_id", server.GetID())
						return
					}
				}
			}
		}(payload)
	}
}
