package broker

import (
	"testing"
)

func TestBroker(t *testing.T) {
	// signals := make(chan os.Signal, 1)
	// signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Initialize configuration
	// cfg := &config.Config{
	// 	Port: 5672,
	// }

	// // Initialize the message broker
	// b := broker.NewBroker(cfg)

	// b.AddExchange("OrderExchange", broker.DIRECT)
	// b.AddQueue("OrderQueue")
	// b.BindQueue("OrderExchange", "OrderQueue")

	// go b.Start()
	// err := b.RouteMessage("orderExchange", "orderQueue", "Order created")
	// if err != nil {
	// 	fmt.Println("Error routing message:", err)
	// }

}
