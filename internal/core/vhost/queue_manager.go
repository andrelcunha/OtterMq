package vhost

import (
	"sync"

	"github.com/andrelcunha/ottermq/pkg/common/communication/amqp"
)

type Queue struct {
	Name       string     `json:"name"`
	Durable    bool       `json:"durable"`
	Exclusive  bool       `json:"exclusive"`
	AutoDelete bool       `json:"auto_delete"`
	MessageTTL int        `json:"message_ttl"`
	Arguments  QueueArgs  `json:"arguments"`
	head       *Node      `json:"-"` // pointer to the first message in the queue
	mu         sync.Mutex `json:"-"`
}

type QueueArgs map[string]interface{}

type Node struct {
	next *Node
	data amqp.Message
}

func NewQueue(name string) *Queue {
	queue := &Queue{
		Name: name,
		// messages: make(chan Message, 100),
	}
	return queue
}

func (q *Queue) Push(msg amqp.Message) {
	// queue.messages <- msg
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.head == nil {
		q.head = &Node{data: msg}
		return
	}

}

func (q *Queue) Pop() *amqp.Message {
	// return <-queue.messages
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.head == nil {
		return nil
	}
	head := q.head
	q.head = head.next
	return &head.data
}

func (q *Queue) ReQueue(msg amqp.Message) {
	q.mu.Lock()
	defer q.mu.Unlock()
	node := &Node{data: msg}
	node.next = q.head
	q.head = node
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	count := 0
	for node := q.head; node != nil; node = node.next {
		count++
	}
	return count
}
