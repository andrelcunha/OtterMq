package main

import (
	"github.com/andrelcunha/ottermq/pkg/connection/client"

	"github.com/andrelcunha/ottermq/pkg/connection/shared"
)

var (
	version = "0.6.0-alpha"
)

const (
	PORT      = "5673"
	HOST      = "localhost"
	HEARTBEAT = 10
	USERNAME  = "guest"
	PASSWORD  = "guest"
	VHOST     = "/"
)

func main() {
	config := &shared.ClientConfig{
		Host:     HOST,
		Port:     PORT,
		Username: USERNAME,
		Password: PASSWORD,
		Vhost:    VHOST,
	}
	client := client.NewClient(config)
	client.Start()
}
