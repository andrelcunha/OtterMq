package ottermqclient

import (
	"bufio"
	"fmt"
	"net"
)

type OtterMqClient struct {
	conn   net.Conn
	reader *bufio.Reader
}

func NewClient(address string) (*OtterMqClient, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	return &OtterMqClient{
		conn:   conn,
		reader: bufio.NewReader(conn),
	}, err
}

func (c *OtterMqClient) SendMessage(message string) (string, error) {
	_, err := c.conn.Write([]byte(message + "\n"))
	if err != nil {
		return "", err
	}

	response, err := c.reader.ReadString('\n')
	if err != nil {
		return "", err
	}

	return response, err
}

func (c *OtterMqClient) CreateExchange(name string, typ string) (string, error) {
	if typ != "DIRECT" && typ != "TOPIC" && typ != "FANOUT" && typ != "HEADERS" {
		return "", fmt.Errorf("invalid exchange type: %s", typ)
	}
	command := fmt.Sprintf("CREATE_EXCHANGE %s %s", name, typ)
	return c.SendMessage(command)
}

func (c *OtterMqClient) CreateQueue(name string) (string, error) {
	command := fmt.Sprintf("CREATE_QUEUE %s", name)
	return c.SendMessage(command)
}

func (c *OtterMqClient) BindQueue(exchangeName, queueName string) (string, error) {
	command := fmt.Sprintf("BIND_QUEUE %s %s", exchangeName, queueName)
	return c.SendMessage(command)
}

func (c *OtterMqClient) Close() {
	c.conn.Close()
}
