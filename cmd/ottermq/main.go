package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/andrelcunha/ottermq/internal/broker"
	"github.com/andrelcunha/ottermq/pkg/config"
)

func main() {
	fmt.Println("Starting OtterMq...")

	// Handle OS signals for graceful shutdown
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Initialize configuration
	config := &config.Config{
		Port: 5672,
	}
	fmt.Println("Config Port: ", config.Port)

	// Initialize the message broker
	broker := broker.NewBroker(config)
	go broker.Start()

	// Wait for a signal to shutdown
	<-signals
	fmt.Println("Shutting down OtterMq...")
	broker.Stop()
}
