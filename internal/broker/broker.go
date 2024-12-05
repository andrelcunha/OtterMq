package broker

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/google/uuid"
)

type Broker struct {
	connections     map[net.Conn]bool
	exchanges       map[string]*Exchange
	queues          map[string]*Queue
	unackedMessages map[string]Message
	mu              sync.Mutex
}

type Exchange struct {
	name     string
	queues   map[string]*Queue
	typ      ExchangeType
	bindings map[string][]*Queue
}

type ExchangeType string

const (
	Direct ExchangeType = "direct"
	Fanout ExchangeType = "fanout"
)

type Queue struct {
	name     string
	messages chan Message
}

type Message struct {
	ID      string
	Content string
}

func NewBroker() *Broker {
	return &Broker{
		connections:     make(map[net.Conn]bool),
		exchanges:       make(map[string]*Exchange),
		queues:          make(map[string]*Queue),
		unackedMessages: make(map[string]Message),
	}
}

func (b *Broker) handleConnection(conn net.Conn) {
	defer conn.Close()
	b.mu.Lock()
	b.connections[conn] = true
	b.mu.Unlock()
	log.Println("New connection established")

	reader := bufio.NewReader(conn)
	for {
		msg, err := reader.ReadString('\n')
		if err != nil {
			log.Println("Connection closed")
			break
		}

		log.Printf("Received: %s\n", msg)
		response, err := b.processCommand(msg)
		if err != nil {
			log.Println("Error processing command:", err)
			continue
		}

		// Write the response back to the client
		_, err = conn.Write([]byte(response + "\n"))
		if err != nil {
			log.Println("Failed to write response:", err)
			break
		}
	}
}

func (b *Broker) Start(addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to start broker: %v", err)
	}
	defer listener.Close()
	log.Printf("Broker listening on %s", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Failed to accept connection:", err)
			continue
		}
		go b.handleConnection(conn)
	}
}

func (b *Broker) processCommand(command string) (string, error) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("Invalid command")
	}

	switch parts[0] {
	case "CREATE_EXCHANGE":
		if len(parts) != 3 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		exchangeName := parts[1]
		typ := parts[2]
		b.CreateExchange(exchangeName, ExchangeType(typ))
		return fmt.Sprintf("Exchange %s of type %s created", exchangeName, typ), nil

	case "CREATE_QUEUE":
		if len(parts) != 2 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		queueName := parts[1]
		b.CreateQueue(queueName)
		return fmt.Sprintf("Queue %s created", queueName), nil

	case "BIND_QUEUE":
		if len(parts) < 3 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		exchangeName := parts[1]
		queueName := parts[2]
		routingKey := ""
		if len(parts) == 4 {
			routingKey = parts[3]
		}
		err := b.BindQueue(exchangeName, queueName, routingKey)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Queue %s bound to exchange %s", queueName, exchangeName), err

	case "PUBLISH":
		if len(parts) < 4 {
			fmt.Printf("Length: %d", len(parts))
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		exchangeName := parts[1]
		routingKey := parts[2]
		message := strings.Join(parts[3:], " ")
		b.Publish(exchangeName, routingKey, message)
		return "Message sent", nil

	case "CONSUME":
		if len(parts) != 2 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		queueName := parts[1]
		msg := <-b.Consume(queueName)
		return msg.Content, nil

	case "ACK":
		if len(parts) != 2 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		msgID := parts[1]
		b.Acknowledge(msgID)
		return fmt.Sprintf("Message ID %s acknowledged", msgID), nil

	case "DELETE_QUEUE":
		if len(parts) != 2 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		queueName := parts[1]
		b.DeleteQueue(queueName)
		return fmt.Sprintf("Queue %s deleted", queueName), nil

	case "LIST_QUEUES":
		if len(parts) != 1 {
			return "", fmt.Errorf("Invalid %s command", parts[0])
		}
		queueNames := b.ListQueues()
		queues := strings.Join(queueNames, ", ")
		return fmt.Sprintf("Queues: %s", queues), nil

	default:
		return "", fmt.Errorf("Unknown command '%s'", parts[0])
	}
}

func (b *Broker) Acknowledge(msgID string) {
	b.mu.Lock()
	delete(b.unackedMessages, msgID)
	b.mu.Unlock()
}

func (b *Broker) CreateQueue(name string) *Queue {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := &Queue{
		name:     name,
		messages: make(chan Message, 100),
	}
	b.queues[name] = queue
	return queue
}

func (b *Broker) Publish(exchangeName, routingKey, message string) {
	b.mu.Lock()
	exchange, ok := b.exchanges[exchangeName]
	b.mu.Unlock()
	if !ok {
		log.Printf("Exchange %s not found", exchangeName)
		return
	}
	msgID := uuid.New().String()
	msg := Message{
		ID:      msgID,
		Content: message,
	}

	switch exchange.typ {
	case Direct:
		queues, ok := exchange.bindings[routingKey]
		if ok {
			for _, queue := range queues {
				queue.messages <- msg
				log.Printf("Message %s sent to queue %s", msgID, queue.name)
			}
		} else {
			log.Printf("Queue %s not found for routing key %s", routingKey, exchangeName)
			return
		}
	case Fanout:
		for _, queue := range exchange.queues {
			queue.messages <- msg
			log.Printf("Message %s sent to queue %s", msgID, queue.name)
		}
	}
}

func (b *Broker) Consume(queueName string) <-chan Message {
	b.mu.Lock()
	queue, ok := b.queues[queueName]
	b.mu.Unlock()
	if !ok {
		log.Printf("Queue %s not found", queueName)
		return nil
	}
	return queue.messages
}

func (b *Broker) DeleteQueue(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.queues, name)
}

func (b *Broker) ListQueues() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	queueNames := make([]string, 0, len(b.queues))
	for name := range b.queues {
		queueNames = append(queueNames, name)
	}
	return queueNames
}

func (b *Broker) CreateExchange(name string, typ ExchangeType) {
	b.mu.Lock()
	defer b.mu.Unlock()
	exchange := &Exchange{
		name:     name,
		typ:      typ,
		queues:   make(map[string]*Queue),
		bindings: make(map[string][]*Queue),
	}
	b.exchanges[name] = exchange
}

func (b *Broker) BindQueue(exchangeName, queueName, routingKey string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	exchange, ok := b.exchanges[exchangeName]
	if !ok {
		return fmt.Errorf("Exchange %s not found", exchangeName)
	}
	queue, ok := b.queues[queueName]
	if !ok {
		return fmt.Errorf("Queue %s not found", queueName)
	}

	switch exchange.typ {
	case Direct:
		if routingKey == "" {
			return fmt.Errorf("Routing key is required for direct exchange")
		}
		exchange.bindings[routingKey] = append(exchange.bindings[routingKey], queue)
	case Fanout:
		exchange.queues[queueName] = queue
	}

	return nil
}
