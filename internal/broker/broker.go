package broker

import (
	"fmt"
	"net"
	"strconv"

	"github.com/andrelcunha/ottermq/pkg/config"
	"github.com/andrelcunha/ottermq/pkg/libs/models"
)

type Broker struct {
	listener  net.Listener
	clients   []net.Conn
	config    *config.Config
	exchanges map[string]*models.Exchange
	queues    map[string]*models.Queue
}

func NewBroker(config *config.Config) *Broker {
	return &Broker{
		config:    config,
		exchanges: make(map[string]*models.Exchange),
		queues:    make(map[string]*models.Queue),
	}
}

// Start begins the message broker
func (b *Broker) Start() {
	var err error
	host := ":" + strconv.Itoa(b.config.Port)
	b.listener, err = net.Listen("tcp", host)
	if err != nil {
		fmt.Println("Error starting broker:", err)
		return
	}

	fmt.Printf("Broker started on port %d\n", b.config.Port)

	for {
		conn, err := b.listener.Accept()
		if err != nil {
			if err.Error() != "use of closed network connection" {
				fmt.Println("Error accepting connection:", err)
				continue
			}
		}
		b.clients = append(b.clients, conn)
		go b.handleClient(conn)
	}
}

// Stop gracefully stops the message broker
func (b *Broker) Stop() {
	for _, client := range b.clients {
		client.Close()
	}
	b.listener.Close()
}

// handleClient handles a client connection
func (b *Broker) handleClient(conn net.Conn) {
	defer conn.Close()

	buf := make([]byte, 1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			if err.Error() == "EOF" {
				// fmt.Println("Client disconnected")
			} else {
				fmt.Println("Error reading from client:", err)
			}
			break
		}
		message := string(buf[:n])
		fmt.Printf("Received: %s\n", message)

		response := fmt.Sprintf("Message received: %s", message)
		conn.Write([]byte(response + "\n"))
	}
}

func (b *Broker) AddExchange(name string, typ models.ExchangeType) {
	b.exchanges[name] = &models.Exchange{
		Name: name,
		Type: typ,
	}
}

func (b *Broker) AddQueue(name string) {
	b.queues[name] = &models.Queue{
		Name:     name,
		Messages: []string{},
	}
}

func (b *Broker) BindQueue(exchangeName string, queueName string) error {
	exchange, exists := b.exchanges[exchangeName]
	if !exists {
		return fmt.Errorf("exchange %s does not found", exchangeName)
	}

	queue, exists := b.queues[queueName]
	if !exists {
		return fmt.Errorf("queue %s does not found", queueName)
	}

	exchange.Queues = append(exchange.Queues, queue)
	return nil
}

func (b *Broker) RouteMessage(exchangeName, routingKey, message string) error {
	exchange, exists := b.exchanges[exchangeName]
	if !exists {
		return fmt.Errorf("exchange %s does not found", exchangeName)
	}

	for _, queue := range exchange.Queues {
		if queue.Name == routingKey {
			queue.Messages = append(queue.Messages, message)
			return nil
		}
	}
	return fmt.Errorf("no matching queue found for routing key %s", routingKey)
}
