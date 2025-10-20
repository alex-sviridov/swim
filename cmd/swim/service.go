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
	"github.com/alex-sviridov/swim/internal/provisioner"
	"github.com/alex-sviridov/swim/internal/redis"
)

const (
	provisionQueueKey = "swim:provision:queue"
	queueTimeout      = 30 * time.Second
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

	// Create provisioner
	prov := provisioner.New(log, conn, redisClient)

	// Main queue processing loop
	for {
		// Check if shutdown was requested
		select {
		case <-ctx.Done():
			log.Info("waiting for active provisioning tasks to complete")
			wg.Wait()
			log.Info("all tasks completed, shutting down")
			return
		default:
		}

		// Pop payload from Redis queue (blocking)
		payload, err := redisClient.PopPayload(ctx, provisionQueueKey, queueTimeout)
		if err != nil {
			log.Debug("failed to pop payload from queue", "error", err)
			continue
		}

		log.Info("received provision request", "payload_length", len(payload))

		// Process in a goroutine
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			prov.ProcessRequest(ctx, payload)
		}(payload)
	}
}
