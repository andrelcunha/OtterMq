package ottermqclient

import "net"

type OtterMqClient struct {
	conn net.Conn
}

func NewClient(address string) (*OtterMqClient, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	return &OtterMqClient{conn: conn}, err
}

func (c *OtterMqClient) SendMessage(message string) (string, error) {
	_, err := c.conn.Write([]byte(message + "\n"))
	if err != nil {
		return "", err
	}

	buf := make([]byte, 1024)
	n, err := c.conn.Read(buf)
	if err != nil {
		return "", err
	}

	return string(buf[:n]), err
}

func (c *OtterMqClient) Close() {
	c.conn.Close()
}
