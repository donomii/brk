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

	delivered := make(chan brk.UdpMessage, 1)
	receiver, err := brk.StartUdpContext(ctx, "127.0.0.1", "0", func(incoming, outgoing chan brk.UdpMessage) {
		seen := map[int]int{}
		for message := range incoming {
			seen[message.Sequence] = seen[message.Sequence] + 1
			fmt.Printf("receiver saw sequence %d attempt %d\n", message.Sequence, seen[message.Sequence])
			if seen[message.Sequence] == 1 {
				fmt.Printf("receiver skipped first acknowledgement for sequence %d\n", message.Sequence)
			} else {
				outgoing <- brk.UdpMessage{Address: message.Address, Port: message.Port, Sequence: message.Sequence, Type: "Ack", Cached: time.Now()}
				delivered <- message
				return
			}
		}
	})
	if err != nil {
		panic(err)
	}
	defer closeUdpServer(receiver)

	config := brk.RetryConfig{QueueLength: 20, RetryInterval: 50 * time.Millisecond, AckTimeout: 20 * time.Millisecond, MaxAttempts: 5, DuplicateTTL: time.Minute}
	sender, err := brk.StartRetryUdpContext(ctx, "127.0.0.1", "0", config, func(incoming, outgoing chan brk.UdpMessage) {})
	if err != nil {
		panic(err)
	}
	defer closeRetryServer(sender)

	host, port := splitAddress(receiver.LocalAddress())
	brk.SendMessage(sender.Outgoing, []byte("retry demo"), host, port)

	message := receive(delivered)
	waitForRetryAndAck(sender)
	fmt.Printf("delivered after retry: %s\n", string(message.Data))
	fmt.Printf("sender stats: %+v\n", sender.Stats())
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

func receive(messages chan brk.UdpMessage) brk.UdpMessage {
	select {
	case message := <-messages:
		return message
	case <-time.After(2 * time.Second):
		panic("lossy demo did not receive a retried message")
	}
}

func waitForRetryAndAck(server *brk.RetryUdpServer) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stats := server.Stats()
		if stats.Retried > 0 && stats.Acked > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	panic(fmt.Sprintf("sender did not record retry and acknowledgement: stats=%+v", server.Stats()))
}

func closeUdpServer(server *brk.UdpServer) {
	err := server.Close()
	if err != nil {
		panic(err)
	}
}

func closeRetryServer(server *brk.RetryUdpServer) {
	err := server.Close()
	if err != nil {
		panic(err)
	}
}
