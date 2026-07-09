package main

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/donomii/brk"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	config := brk.RetryConfig{QueueLength: 20, RetryInterval: 50 * time.Millisecond, AckTimeout: 20 * time.Millisecond, MaxAttempts: 5, DuplicateTTL: time.Minute}
	receivedByA := make(chan brk.UdpMessage, 1)
	receivedByB := make(chan brk.UdpMessage, 1)

	serverA, err := brk.StartRetryUdpContext(ctx, "127.0.0.1", "0", config, func(incoming, outgoing chan brk.UdpMessage) {
		receivedByA <- <-incoming
	})
	if err != nil {
		panic(err)
	}
	defer closeRetryServer(serverA)

	serverB, err := brk.StartRetryUdpContext(ctx, "127.0.0.1", "0", config, func(incoming, outgoing chan brk.UdpMessage) {
		receivedByB <- <-incoming
	})
	if err != nil {
		panic(err)
	}
	defer closeRetryServer(serverB)

	hostA, portA := splitAddress(serverA.LocalAddress())
	hostB, portB := splitAddress(serverB.LocalAddress())
	brk.SendMessage(serverA.Outgoing, []byte("hello from A"), hostB, portB)
	brk.SendMessage(serverB.Outgoing, []byte("hello from B"), hostA, portA)

	messageForA := receive("A", receivedByA)
	messageForB := receive("B", receivedByB)
	fmt.Printf("A received: %s\n", string(messageForA.Data))
	fmt.Printf("B received: %s\n", string(messageForB.Data))
	waitForAck(serverA)
	waitForAck(serverB)
	fmt.Printf("A stats: %+v\n", serverA.Stats())
	fmt.Printf("B stats: %+v\n", serverB.Stats())
}

func splitAddress(address string) (string, int) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		panic(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		panic(err)
	}
	return host, port
}

func receive(name string, messages chan brk.UdpMessage) brk.UdpMessage {
	select {
	case message := <-messages:
		return message
	case <-time.After(2 * time.Second):
		panic(fmt.Sprintf("%s did not receive a message", name))
	}
}

func waitForAck(server *brk.RetryUdpServer) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if server.Stats().Acked > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	panic(fmt.Sprintf("server %s did not receive an acknowledgement", server.LocalAddress()))
}

func closeRetryServer(server *brk.RetryUdpServer) {
	err := server.Close()
	if err != nil {
		panic(err)
	}
}
