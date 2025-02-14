package broker

import (
	"fmt"

	"github.com/andrelcunha/ottermq/internal/core/vhost"
	. "github.com/andrelcunha/ottermq/pkg/common"
)

func ListConnections(b *Broker) []ConnectionInfoDTO {
	b.mu.Lock()
	defer b.mu.Unlock()
	connections := make([]ConnectionInfo, 0, len(b.Connections))
	for _, c := range b.Connections {
		connections = append(connections, *c)
	}
	connectionsDTO := mapListConnectionsDTO(connections)
	return connectionsDTO
}

func mapListConnectionsDTO(connections []ConnectionInfo) []ConnectionInfoDTO {
	listConnectonsDTO := make([]ConnectionInfoDTO, len(connections))
	for i, connection := range connections {
		state := "disconnected"
		if connection.Done == nil {
			state = "running"
		}
		channels := len(connection.Channels)
		listConnectonsDTO[i] = ConnectionInfoDTO{
			VHostName:     connection.VHostName,
			VHostId:       connection.VHostId,
			Name:          connection.Name,
			Username:      connection.User,
			State:         state,
			SSL:           false,
			Protocol:      "AMQP 0-9-1",
			Channels:      channels,
			LastHeartbeat: connection.LastHeartbeat,
			ConnectedAt:   connection.ConnectedAt,
		}
	}
	return listConnectonsDTO
}

func ListExchanges(b *Broker) []ExchangeDTO {
	exchanges := make([]ExchangeDTO, 0, b.GetTotalExchanges())
	b.mu.Lock()
	defer b.mu.Unlock()
	for vhostName := range b.VHosts {
		vhost := b.VHosts[vhostName]
		for _, exchange := range b.VHosts[vhost.Name].Exchanges {
			exchanges = append(exchanges, ExchangeDTO{
				VHostName: vhost.Name,
				VHostId:   vhost.Id,
				Name:      exchange.Name,
				Type:      string(exchange.Typ),
			})
		}
	}
	return exchanges
}

func ListQueues(b *Broker) []QueueDTO {
	queues := make([]QueueDTO, 0, b.GetTotalQueues())
	b.mu.Lock()
	defer b.mu.Unlock()
	for vhostName := range b.VHosts {
		vhost := b.VHosts[vhostName]
		for _, queue := range b.VHosts[vhost.Name].Queues {
			queues = append(queues, QueueDTO{
				VHostName: vhost.Name,
				VHostId:   vhost.Id,
				Name:      queue.Name,
				Messages:  queue.Len(),
			})
		}
	}
	return queues
}

func ListBindings(b *Broker, vhostName, exchangeName string) map[string][]string {
	vh := b.GetVHostFromName(vhostName)
	b.mu.Lock()
	defer b.mu.Unlock()
	if vh == nil {
		return nil
	}
	fmt.Printf("exchangeName: %s, Vhost: %s", exchangeName, vh.Name)
	exchange, ok := vh.Exchanges[exchangeName]
	if !ok {
		return nil
	}

	switch exchange.Typ {
	case vhost.DIRECT:
		bindings := make(map[string][]string)
		for routingKey, queues := range exchange.Bindings {
			var queuesStr []string
			for _, queue := range queues {
				queuesStr = append(queuesStr, queue.Name)
			}
			bindings[routingKey] = queuesStr
		}
		return bindings
	case vhost.FANOUT:
		bindings := make(map[string][]string)
		var queues []string
		for queueName := range exchange.Queues {
			queues = append(queues, queueName)
		}
		bindings["fanout"] = queues
		return bindings
	}
	return nil
}
