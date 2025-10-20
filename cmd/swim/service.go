package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/alex-sviridov/swim/internal/cleanup"
	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/alex-sviridov/swim/internal/decommissioner"
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
)

const (
	provisionQueueKey   = "swim:provision:queue"
	decommissionQueueKey = "swim:decomission:queue"
	queueTimeout        = 30 * time.Second
)

// runQueueProcessor orchestrates the queue processing and cleanup workers
func runQueueProcessor(log *slog.Logger, conn connector.Connector, redisClient redis.ClientInterface) {
	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	var wg sync.WaitGroup

	// Start cleanup worker
	cleanupWorker := cleanup.New(log, conn, redisClient)
	go cleanupWorker.Run(ctx)

	// Start shutdown handler
	go func() {
		<-sigChan
		log.Info("shutdown signal received, stopping gracefully")
		cancel()
	}()

	// Create provisioner and decommissioner
	prov := provisioner.New(log, conn, redisClient)
	decomm := decommissioner.New(log, conn, redisClient)

	// Start provision queue processor
	go processQueue(ctx, &wg, log, redisClient, provisionQueueKey, "provision", func(payload string) {
		prov.ProcessRequest(ctx, payload)
	})

	// Start decommission queue processor
	go processQueue(ctx, &wg, log, redisClient, decommissionQueueKey, "decommission", func(payload string) {
		decomm.ProcessRequest(ctx, payload)
	})

	// Wait for shutdown signal
	<-ctx.Done()
	log.Info("waiting for active tasks to complete")
	wg.Wait()
	log.Info("all tasks completed, shutting down")
}

// processQueue processes requests from a Redis queue
func processQueue(ctx context.Context, wg *sync.WaitGroup, log *slog.Logger, redisClient redis.ClientInterface, queueKey string, queueType string, handler func(string)) {
	for {
		// Check if shutdown was requested
		select {
		case <-ctx.Done():
			log.Info("queue processor stopping", "queue_type", queueType)
			return
		default:
		}

		// Pop payload from Redis queue (blocking)
		payload, err := redisClient.PopPayload(ctx, queueKey, queueTimeout)
		if err != nil {
			log.Debug("failed to pop payload from queue", "queue_type", queueType, "error", err)
			continue
		}

		log.Info("received request", "queue_type", queueType, "payload_length", len(payload))

		// Process in a goroutine
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			handler(payload)
		}(payload)
	}
}
