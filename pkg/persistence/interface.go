package persistence

// Persistence defines the interface for all storage backends.
//
// Concurrency: implementations must be safe for concurrent use by multiple
// goroutines. The broker calls them without holding a common lock; for
// example, a publish calls SaveMessage while an ack on another channel calls
// DeleteMessage on the same queue. Each method must be atomic with respect to
// every other method that touches the same entity (a queue with its
// messages, or an exchange with its bindings): concurrent calls must not lose
// each other's updates, and a load must never observe a partially written
// entity. The order in which concurrent calls take effect is unspecified.
type Persistence interface {
	/* Queue operations */

	// SaveQueueMetadata saves a single queue's metadata to a JSON file (props)
	SaveQueueMetadata(vhost, name string, props QueueProperties) error
	// LoadQueueMetadata loads a single queue's metadata from a JSON file (props)
	LoadQueueMetadata(vhost, name string) (props QueueProperties, err error)
	// DeleteQueueMetadata deletes a single queue's metadata file
	DeleteQueueMetadata(vhost, name string) error

	/* Exchange operations */
	// SaveExchangeMetadata saves exchange metadata to a JSON file (type and properties)
	SaveExchangeMetadata(vhost, name, exchangeType string, props ExchangeProperties) error
	// LoadExchangeMetadata loads exchange metadata from a JSON file (type and properties)
	LoadExchangeMetadata(vhost, name string) (exchangeType string, props ExchangeProperties, err error)
	// DeleteExchangeMetadata deletes a single exchange's metadata file
	DeleteExchangeMetadata(vhost, name string) error

	// Binding operations
	// SaveBindingState saves a binding between an exchange and a queue
	SaveBindingState(vhost, exchange, queue, routingKey string, arguments map[string]any) error
	// LoadExchangeBindings loads a binding between an exchange and a queue
	LoadExchangeBindings(vhost, exchange string) ([]BindingData, error)
	// DeleteBindingState deletes a binding between an exchange and a queue
	DeleteBindingState(vhost, exchange, queue, routingKey string, arguments map[string]any) error

	// Message operations (to be defined)
	SaveMessage(vhost, queue, msgId string, msgBody []byte, msgProps MessageProperties) error
	LoadMessages(vhostName, queueName string) ([]Message, error)
	DeleteMessage(vhost, queue, msgId string) error

	// Bulk recovery methods
	LoadAllExchanges(vhost string) ([]ExchangeSnapshot, error)
	LoadAllQueues(vhost string) ([]QueueSnapshot, error)

	// VHost operations
	SaveVHostMetadata(name string) error
	DeleteVHostMetadata(name string) error
	LoadAllVHosts() ([]string, error)

	// Lifecycle
	Initialize() error
	Close() error

	// Health/Status (TBD)
	// HealthCheck() error
}

// Config for persistence implementations
type Config struct {
	Type    string            `json:"type"`     // "json", "memento", etc.
	DataDir string            `json:"data_dir"` // Base directory for storage
	Options map[string]string `json:"options"`  // Implementation-specific options
}
