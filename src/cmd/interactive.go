package main

import (
	"fmt"
	"log/slog"

	"github.com/alex-sviridov/swim/internal/connector"
)

// runInteractiveMode handles one-shot JSON payload processing
func runInteractiveMode(log *slog.Logger, conn connector.Connector, payload string) {
	log.Info("Running in interactive mode", "payload", payload)

	// Provision the instance
	instance, err := conn.ProvisionInstance(payload)
	if err != nil {
		log.Error("Failed to provision instance", "error", err)
		return
	}
	fmt.Printf("Provisioned instance: %+v\n", instance)
}
