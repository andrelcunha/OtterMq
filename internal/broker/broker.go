package broker

import (
	"bufio"
	"fmt"
	"net"
	"strconv"

	"github.com/andrelcunha/ottermq/pkg/config"
)

type Broker struct {
	listener net.Listener
	clients  []net.Conn
	config   *config.Config
}

func NewBroker(config *config.Config) *Broker {
	return &Broker{
		config: config,
	}
}

// Start begins the message broker
func (b *Broker) Start() {
	var err error
	host := ":" + strconv.Itoa(b.config.Port)
	fmt.Printf("host: %s\n", host)
	fmt.Printf("port: %d\n", b.config.Port)
	b.listener, err = net.Listen("tcp", host)
	if err != nil {
		fmt.Println("Error starting broker:", err)
		return
	}

	fmt.Printf("Broker started on port %d\n", b.config.Port)

	for {
		conn, err := b.listener.Accept()
		if err != nil {
			fmt.Println("Error accepting connection:", err)
			continue
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

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		message := scanner.Text()
		fmt.Printf("Received: %s\n", message)

		// Process message (e.g., route to appropriate queue)
		// Placeholder response
		response := fmt.Sprintf("Message received: %s", message)
		conn.Write([]byte(response + "\n"))
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading from client:", err)
	}
}
