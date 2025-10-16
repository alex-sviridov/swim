package main

import (
	"fmt"
	"log/slog"

	"github.com/alex-sviridov/swim/internal/connector"
)

// runInteractiveMode handles one-shot JSON payload processing
func runInteractiveMode(log *slog.Logger, conn connector.Connector, payload string) {
	log.Info("Running in interactive mode", "payload", payload)

	// Provision the server
	server, err := conn.CreateServer(payload)
	if err != nil {
		log.Error("Failed to provision server", "error", err)
		return
	}
	fmt.Printf("Provisioned server: %+v\n", server)
}
