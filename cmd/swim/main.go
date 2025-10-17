package main

import (
	"flag"
	"os"
    "github.com/joho/godotenv"

	"github.com/alex-sviridov/swim/internal/connector/scaleway"
	"github.com/alex-sviridov/swim/internal/logger"
)

func main() {
	// Load .env file if it exists (ignore error if file doesn't exist)
	_ = godotenv.Load()

	// Define CLI flags
	interactive := flag.String("interactive", "", "JSON payload for one-shot interactive mode")
	redis := flag.String("redis", "", "Redis connection string for service mode")
	verbose := flag.Bool("verbose", false, "Enable verbose logging (info level)")
	dryrun := flag.Bool("dry-run", false, "Dry-run without creating a real instance")
	flag.Parse()

	// Initialize logger
	log := logger.New(*verbose)

	// Validate that exactly one mode is specified
	if (*interactive == "" && *redis == "") || (*interactive != "" && *redis != "") {
		log.Error("Error: specify exactly one of --interactive or --redis")
		os.Exit(1)
	}

	// Create the SCW connection
	conn, err := scaleway.NewConnector(*dryrun)
	if err != nil {
		log.Error("Error connecting to Scaleway", "error", err)
		os.Exit(1)
	}

	// Run in the appropriate mode
	if *interactive != "" {
		runInteractiveMode(log, conn, *interactive)
	} else {
		runRedisMode(log, conn, *redis)
	}
}
