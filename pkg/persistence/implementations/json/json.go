package json

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ottermq/ottermq/pkg/persistence"
)

// fileLockStripes is the number of locks entity files are spread over.
const fileLockStripes = 256

// JsonPersistence keeps one JSON file per queue and per exchange. It is safe
// for concurrent use: every read, write and load-modify-save of a file holds
// that file's lock, so concurrent updates to one entity (publish and ack on a
// queue, two binds on an exchange) are serialised instead of interleaved.
type JsonPersistence struct {
	dataDir string
	// fileLocks is a striped lock keyed by file path. Unrelated files may
	// share a stripe, which only serialises them. No method holds more than
	// one stripe at a time.
	fileLocks [fileLockStripes]sync.Mutex
}

func NewJsonPersistence(config *persistence.Config) (*JsonPersistence, error) {
	jp := &JsonPersistence{
		dataDir: config.DataDir,
	}
	return jp, jp.Initialize()
}

// equalArgs compares two argument maps by their JSON encoding to avoid type
// discrepancies (e.g., int vs float64) after JSON round-trips.
func equalArgs(a, b map[string]any) bool {
	// Quick path
	if a == nil && b == nil {
		return true
	}
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

func (jp *JsonPersistence) Initialize() error {
	// Create the base data directory if it doesn't exist
	if err := mkdirAllDurable(jp.dataDir, 0755); err != nil {
		return err
	}
	if err := jp.checkLayout(); err != nil {
		return err
	}
	// Discard writes a crash interrupted before their rename
	return removeStrayTempFiles(jp.vhostsDir())
}

func (jp *JsonPersistence) Close() error {
	// JSON implementation doesn't need to clean up
	return nil
}

// lockFile locks the stripe that guards path and returns its unlock function.
func (jp *JsonPersistence) lockFile(path string) func() {
	h := fnv.New32a()
	h.Write([]byte(path))
	mu := &jp.fileLocks[h.Sum32()%fileLockStripes]
	mu.Lock()
	return mu.Unlock
}

func (jp *JsonPersistence) exchangeFile(vhost, name string) string {
	return filepath.Join(jp.exchangesDir(vhost), encodeName(name)+".json")
}

func (jp *JsonPersistence) queueFile(vhost, name string) string {
	return filepath.Join(jp.queuesDir(vhost), encodeName(name)+".json")
}

// SaveExchangeMetadata persists exchange metadata to a JSON file
func (jp *JsonPersistence) SaveExchangeMetadata(vhost, name, exchangeType string, props persistence.ExchangeProperties) error {
	if err := jp.ensureVHostDir(vhost); err != nil {
		return err
	}
	if err := mkdirAllDurable(jp.exchangesDir(vhost), 0755); err != nil {
		return err
	}

	exchangeData := JsonExchangeData{
		Name:       name,
		Type:       exchangeType,
		Properties: props,
	}
	file := jp.exchangeFile(vhost, name)
	defer jp.lockFile(file)()
	data, err := json.MarshalIndent(exchangeData, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(file, data, 0644)
}

// LoadExchangeMetadata loads exchange metadata from a JSON file (type and properties)
func (jp *JsonPersistence) LoadExchangeMetadata(vhost, name string) (exchangeType string, props persistence.ExchangeProperties, err error) {
	file := jp.exchangeFile(vhost, name)
	defer jp.lockFile(file)()

	exchangeData, err := loadExchangeFile(file)
	if err != nil {
		return "", persistence.ExchangeProperties{}, err
	}
	return exchangeData.Type, exchangeData.Properties, nil
}

// DeleteExchange removes a single exchange JSON file
func (jp *JsonPersistence) DeleteExchangeMetadata(vhost, name string) error {
	file := jp.exchangeFile(vhost, name)
	defer jp.lockFile(file)()
	if err := os.Remove(file); err != nil {
		return err
	}
	return nil
}

// SaveQueueMetadata persists a single queue's metadata to a JSON file
func (jp *JsonPersistence) SaveQueueMetadata(vhost, name string, props persistence.QueueProperties) error {
	if err := jp.ensureVHostDir(vhost); err != nil {
		return err
	}
	if err := mkdirAllDurable(jp.queuesDir(vhost), 0755); err != nil {
		return err
	}

	// Update metadata while preserving messages
	queueData := JsonQueueData{
		Name:       name,
		Properties: props,
		Messages:   []JsonMessageData{}, // Preserve existing messages
	}

	file := jp.queueFile(vhost, name)
	defer jp.lockFile(file)()

	// For JSON implementation, we need to preserve existing messages if they exist
	existingQueue, err := loadQueueFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if existingQueue != nil && len(existingQueue.Messages) > 0 {
		queueData.Messages = existingQueue.Messages
	}

	data, err := json.MarshalIndent(queueData, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(file, data, 0644)
}

// LoadQueueMetadata loads a single queue's metadata from a JSON file (props)
func (jp *JsonPersistence) LoadQueueMetadata(vhost, name string) (props persistence.QueueProperties, err error) {
	file := jp.queueFile(vhost, name)
	defer jp.lockFile(file)()
	queueData, err := loadQueueFile(file)
	if err != nil {
		return persistence.QueueProperties{}, err
	}
	return queueData.Properties, nil
}

func (jp *JsonPersistence) SaveBindingState(vhost, exchange, queue, routingKey string, arguments map[string]any) error {
	// For JSON persistence, bindings are part of exchange metadata
	// So we can just load the exchange, update bindings, and save it back
	file := jp.exchangeFile(vhost, exchange)
	defer jp.lockFile(file)()
	existing, err := loadExchangeFile(file)
	if err != nil {
		return fmt.Errorf("failed to load exchange for binding: %v", err)
	}
	exchangeData := JsonExchangeData{
		Name:       exchange,
		Type:       existing.Type,
		Properties: existing.Properties,
		Bindings:   existing.Bindings,
	}

	bindingMetadata := persistence.BindingData{
		QueueName:  queue,
		RoutingKey: routingKey,
		Arguments:  arguments,
	}
	// Deduplicate: avoid adding duplicate binding with same queue, routingKey, and arguments
	duplicate := false
	for _, b := range exchangeData.Bindings {
		if b.QueueName == bindingMetadata.QueueName && b.RoutingKey == bindingMetadata.RoutingKey {
			if equalArgs(b.Arguments, bindingMetadata.Arguments) {
				duplicate = true
				break
			}
		}
	}
	if !duplicate {
		exchangeData.Bindings = append(exchangeData.Bindings, bindingMetadata)
	}

	// Ensure directory exists before writing
	if err := mkdirAllDurable(filepath.Dir(file), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(exchangeData, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(file, data, 0644)
}

func (jp *JsonPersistence) LoadExchangeBindings(vhost string, exchange string) ([]persistence.BindingData, error) {
	file := jp.exchangeFile(vhost, exchange)
	defer jp.lockFile(file)()

	exchangeData, err := loadExchangeFile(file)
	if err != nil {
		return nil, err
	}
	return exchangeData.Bindings, nil
}

// DeleteBindingState deletes a binding between an exchange and a queue
func (jp *JsonPersistence) DeleteBindingState(vhost, exchange, queue, routingKey string, arguments map[string]any) error {
	file := jp.exchangeFile(vhost, exchange)
	defer jp.lockFile(file)()
	existing, err := loadExchangeFile(file)
	if err != nil {
		return fmt.Errorf("failed to load exchange for binding: %v", err)
	}
	// Filter out the binding to be deleted
	var updatedBindings []persistence.BindingData
	for _, b := range existing.Bindings {
		if !(b.QueueName == queue && b.RoutingKey == routingKey && equalArgs(b.Arguments, arguments)) {
			updatedBindings = append(updatedBindings, b)
		}
	}
	// remove the binding from the exchange data
	exchangeData := JsonExchangeData{
		Name:       exchange,
		Type:       existing.Type,
		Properties: existing.Properties,
		Bindings:   updatedBindings,
	}

	// Ensure directory exists before writing (in case metadata was never saved)
	if err := mkdirAllDurable(filepath.Dir(file), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(exchangeData, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(file, data, 0644)
}

func (jp *JsonPersistence) SaveMessage(vhost, queue, msgId string, msgBody []byte, msgProps persistence.MessageProperties) error {
	file := jp.queueFile(vhost, queue)
	defer jp.lockFile(file)()
	qData, err := loadQueueFile(file)
	if err != nil {
		return fmt.Errorf("queue not found: %v", err)
	}

	newMessage := JsonMessageData{
		ID:         msgId,
		Body:       msgBody,
		Properties: msgProps,
	}

	qData.Messages = append(qData.Messages, newMessage)

	// Save the complete queue data including messages
	return saveQueueFile(file, qData)
}

func (jp *JsonPersistence) LoadMessages(vhostName, queueName string) ([]persistence.Message, error) {
	file := jp.queueFile(vhostName, queueName)
	defer jp.lockFile(file)()
	qData, err := loadQueueFile(file)
	if err != nil {
		return nil, fmt.Errorf("queue not found: %v", err)
	}

	var messages []persistence.Message
	for _, msg := range qData.Messages {
		messages = append(messages, persistence.Message{
			ID:         msg.ID,
			Body:       msg.Body,
			Properties: msg.Properties,
		})
	}
	return messages, nil
}

func (jp *JsonPersistence) DeleteMessage(vhost, queue, msgId string) error {
	file := jp.queueFile(vhost, queue)
	defer jp.lockFile(file)()
	qData, err := loadQueueFile(file)
	if err != nil {
		return fmt.Errorf("queue not found: %v", err)
	}

	filtered := qData.Messages[:0]
	removed := false
	for _, msg := range qData.Messages {
		if msg.ID == msgId {
			removed = true
			continue
		}
		filtered = append(filtered, msg)
	}
	if !removed {
		return nil
	}
	qData.Messages = filtered

	return saveQueueFile(file, qData)
}

func (jp *JsonPersistence) LoadAllExchanges(vhost string) ([]persistence.ExchangeSnapshot, error) {
	dir := jp.exchangesDir(vhost)

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []persistence.ExchangeSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}

	var snapshots []persistence.ExchangeSnapshot
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		exchangeData, err := jp.lockedLoadExchangeFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // Skip corrupt files
		}
		name, ok := entityName(entry.Name(), exchangeData.Name)
		if !ok {
			continue // Not a file this layout writes
		}

		snapshots = append(snapshots, persistence.ExchangeSnapshot{
			Name:       name,
			Type:       exchangeData.Type,
			Properties: exchangeData.Properties,
			Bindings:   exchangeData.Bindings,
		})
	}
	return snapshots, nil
}

func (jp *JsonPersistence) LoadAllQueues(vhost string) ([]persistence.QueueSnapshot, error) {
	dir := jp.queuesDir(vhost)

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []persistence.QueueSnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}

	var snapshots []persistence.QueueSnapshot
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		queueData, err := jp.lockedLoadQueueFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue // Skip corrupt files
		}
		name, ok := entityName(entry.Name(), queueData.Name)
		if !ok {
			continue // Not a file this layout writes
		}

		var messages []persistence.Message
		for _, msg := range queueData.Messages {
			messages = append(messages, persistence.Message{
				ID:         msg.ID,
				Body:       msg.Body,
				Properties: msg.Properties,
			})
		}

		snapshots = append(snapshots, persistence.QueueSnapshot{
			Name:       name,
			Properties: queueData.Properties,
			Messages:   messages,
		})
	}
	return snapshots, nil
}

// SaveVHostMetadata ensures the vhost directory exists on disk.
func (jp *JsonPersistence) SaveVHostMetadata(name string) error {
	return jp.ensureVHostDir(name)
}

// DeleteVHostMetadata removes the entire vhost directory tree.
func (jp *JsonPersistence) DeleteVHostMetadata(name string) error {
	dir := jp.vhostDir(name)
	if err := jp.checkVHostDir(dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// LoadAllVHosts returns the names of all persisted vhosts by reading the
// vhosts/ directory. The encoded directory names are decoded back.
func (jp *JsonPersistence) LoadAllVHosts() ([]string, error) {
	dir := jp.vhostsDir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name, ok := jp.vhostName(e.Name())
		if !ok {
			continue // Not a directory this layout writes
		}
		names = append(names, name)
	}
	return names, nil
}

/* ---- Private methods ---- */

// The helpers below don't lock; callers hold the file's lock.

// loadQueueFile loads a single queue from a JSON file
func loadQueueFile(file string) (*JsonQueueData, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var queueData JsonQueueData
	if err := json.Unmarshal(data, &queueData); err != nil {
		return nil, err
	}
	return &queueData, nil
}

func (jp *JsonPersistence) lockedLoadQueueFile(file string) (*JsonQueueData, error) {
	defer jp.lockFile(file)()
	return loadQueueFile(file)
}

// loadExchangeFile loads a single exchange, with its bindings, from a JSON file
func loadExchangeFile(file string) (*JsonExchangeData, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var exchangeData JsonExchangeData
	if err := json.Unmarshal(data, &exchangeData); err != nil {
		return nil, err
	}
	return &exchangeData, nil
}

func (jp *JsonPersistence) lockedLoadExchangeFile(file string) (*JsonExchangeData, error) {
	defer jp.lockFile(file)()
	return loadExchangeFile(file)
}

func saveQueueFile(file string, queueData *JsonQueueData) error {
	if err := mkdirAllDurable(filepath.Dir(file), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(queueData, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(file, data, 0644)
}

// DeleteQueue removes a single queue JSON file
func (jp *JsonPersistence) DeleteQueueMetadata(vhostName, queueName string) error {
	file := jp.queueFile(vhostName, queueName)
	defer jp.lockFile(file)()
	if err := os.Remove(file); err != nil {
		return err
	}
	return nil
}
