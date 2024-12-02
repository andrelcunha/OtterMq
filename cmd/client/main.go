package main

import (
	"fmt"
	"os"

	"github.com/andrelcunha/ottermq/pkg/libs/ottermqclient"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: client <address> <message>")
		return
	}

	address := os.Args[1]
	message := os.Args[2]

	client, err := ottermqclient.NewClient(address)
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		return
	}
	defer client.Close()

	response, err := client.SendMessage(message)
	if err != nil {
		fmt.Println("Error sending message:", err)
		return
	}

	fmt.Println("Response from server:", response)
}
