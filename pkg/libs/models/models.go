package models

type Exchange struct {
	Name   string
	Type   ExchangeType
	Queues []*Queue
}

type Queue struct {
	Name     string
	Messages []string
}

type ExchangeType string

const (
	DIRECT  ExchangeType = "direct"
	TOPIC   ExchangeType = "topic"
	FANOUT  ExchangeType = "fanout"
	HEADERS ExchangeType = "headers"
)
