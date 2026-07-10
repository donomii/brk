package main

import (
	"context"
	"fmt"
	"time"

	"github.com/donomii/brk"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverA := startServer(ctx)
	defer closeServer(serverA)
	serverB := startServer(ctx)
	defer closeServer(serverB)

	fmt.Printf("A local endpoint: %v\n", serverA.LocalEndpoint())
	fmt.Printf("B local endpoint: %v\n", serverB.LocalEndpoint())
	result, err := serverA.PunchPeer(ctx, serverB.LocalEndpoint(), brk.PunchConfig{})
	if err != nil {
		panic(err)
	}
	fmt.Printf("hole punch: peer=%v observed=%v attempts=%d round_trip=%v\n", result.Peer, result.ObservedAddress, result.Attempts, result.RoundTrip)

	keepaliveCtx, stopKeepalive := context.WithTimeout(ctx, 75*time.Millisecond)
	defer stopKeepalive()
	err = serverA.KeepPeerAlive(keepaliveCtx, serverB.LocalEndpoint(), brk.KeepaliveConfig{Interval: 25 * time.Millisecond})
	if err != nil {
		panic(err)
	}
	fmt.Printf("keepalive: peer=%v interval=%v duration=%v\n", serverB.LocalEndpoint(), 25*time.Millisecond, 75*time.Millisecond)
}

func startServer(ctx context.Context) *brk.UdpServer {
	server, err := brk.StartUdpContext(ctx, "127.0.0.1", "0", func(incoming, outgoing chan brk.UdpMessage) {
		for range incoming {
		}
	})
	if err != nil {
		panic(err)
	}
	return server
}

func closeServer(server *brk.UdpServer) {
	err := server.Close()
	if err != nil {
		panic(err)
	}
}
