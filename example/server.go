package main

import (
	"fmt"
)

func processor(incoming, outgoing chan UdpMessage) {
	for mess := range incoming {
		outgoing <- mess
		fmt.Printf("Incoming: %v\n", string(mess.Data))
		outgoing <- UdpMessage{Data: []byte("lalalala"), Address: mess.Address, Port: mess.Port}
	}
}

func main() {
	hostName := "localhost"
	portNum := "6000"
	StartRetryServer(hostName, portNum, processor)
}
