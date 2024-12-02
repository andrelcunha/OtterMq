package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/andrelcunha/ottermq/internal/broker"
	"github.com/andrelcunha/ottermq/pkg/config"
	"github.com/andrelcunha/ottermq/pkg/libs/models"
)

func main() {
	fmt.Println("Starting OtterMq...")

	// Handle OS signals for graceful shutdown
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Initialize configuration
	cfg := &config.Config{
		Port: 5672,
	}

	// Initialize the message broker
	b := broker.NewBroker(cfg)

	// Adding exchanges and queues
	b.AddExchange("orderExchange", models.DIRECT)
	b.AddQueue("orderQueue")
	b.BindQueue("orderExchange", "orderQueue")

	go b.Start()

	// Rooute a test message
	err := b.RouteMessage("orderExchange", "orderQueue", "Order created")
	if err != nil {
		fmt.Println("Error routing message:", err)
	}

	// Wait for a signal to shutdown
	<-signals
	fmt.Println("Shutting down OtterMq...")
	b.Stop()
}
