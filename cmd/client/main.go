package main

import (
	"fmt"
	"os"

	"github.com/andrelcunha/ottermq/pkg/libs/ottermqclient"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: ottermq-client <address> <command> [<args>...]")
		return
	}

	address := os.Args[1]
	command := os.Args[2]
	args := os.Args[3:]

	client, err := ottermqclient.NewClient(address)
	if err != nil {
		fmt.Println("Error connecting to server:", err)
		return
	}
	defer client.Close()

	// response, err := client.SendMessage(message)
	// if err != nil {
	// 	fmt.Println("Error sending message:", err)
	// 	return
	// }

	var response string
	switch command {
	case "create-exchange":
		if len(args) != 2 {
			fmt.Println("Usage: ottermq-client create_exchange <name> <type>")
			return
		}
		response, err = client.CreateExchange(args[0], args[1])

	case "create-queue":
		if len(args) != 1 {
			fmt.Println("Usage: ottermq-client create_queue <name>")
			return
		}
		response, err = client.CreateQueue(args[0])

	case "bind-queue":
		if len(args) != 2 {
			fmt.Println("Usage: ottermq-client bind_queue <exchange> <queue>")
			return
		}
		response, err = client.BindQueue(args[0], args[1])

	default:
		fmt.Println("Unknown command")
		return
	}

	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	fmt.Println("Response from server:", response)
}
