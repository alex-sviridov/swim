package main

import (
	"flag"
	"os"

	"github.com/joho/godotenv"

	"github.com/alex-sviridov/swim/internal/connector/hcloud"
	"github.com/alex-sviridov/swim/internal/logger"
	"github.com/alex-sviridov/swim/internal/redis"
)

func main() {
	// Load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load()

	// Define CLI flags
	redisAddr := flag.String("redis", "", "Redis connection string (required)")
	silent := flag.Bool("silent", false, "Suppress verbose logging (info level)")
	dryrun := flag.Bool("dry-run", false, "Dry-run without creating a real instance")
	flag.Parse()

	// Initialize logger
	log := logger.New(!*silent)

	// Validate redis address
	if *redisAddr == "" {
		*redisAddr = os.Getenv("REDIS_CONNECTION_STRING")
		if *redisAddr == "" {
			log.Error("--redis flag or REDIS_CONNECTION_STRING environment variable is required")
			os.Exit(1)
		}
	}

	// Create Hetzner Cloud connector
	conn, err := hcloud.NewConnector(log, *dryrun)
	if err != nil {
		log.Error("connecting to hetzner cloud", "error", err)
		os.Exit(1)
	}

	// Create Redis client
	redisClient, err := redis.NewClient(redis.Config{
		Address:  *redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
	if err != nil {
		log.Error("failed to connect to redis", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := redisClient.Close(); err != nil {
			log.Error("failed to close redis client", "error", err)
		}
	}()

	log.Info("connected to redis, starting service")

	// Run the queue processor
	runQueueProcessor(log, conn, redisClient)
}
