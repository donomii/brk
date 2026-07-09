[![Go](https://github.com/donomii/brk/actions/workflows/test.yml/badge.svg)](https://github.com/donomii/brk/actions/workflows/test.yml)
[![pkg.go.dev](https://pkg.go.dev/badge/github.com/donomii/brk.svg)](https://pkg.go.dev/github.com/donomii/brk)

# brk

Message-level reliable UDP for Go.

`brk` sends byte messages over UDP without turning them into a stream. The retry server caches outgoing messages, retransmits them when acknowledgements do not arrive, and suppresses duplicate inbound messages.

## Install

```sh
go get github.com/donomii/brk
```

## Quickstart

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/donomii/brk"
)

func main() {
	config := brk.DefaultRetryConfig()
	config.RetryInterval = 250 * time.Millisecond
	config.AckTimeout = 100 * time.Millisecond

	server, err := brk.StartRetryUdpContext(context.Background(), "127.0.0.1", "6000", config, processor)
	if err != nil {
		panic(err)
	}
	defer server.Close()

	brk.SendMessage(server.Outgoing, []byte("hello"), "127.0.0.1", 6001)
	fmt.Printf("listening on %s\n", server.LocalAddress())
}

func processor(incoming, outgoing chan brk.UdpMessage) {
	for message := range incoming {
		fmt.Printf("received from %s:%d: %s\n", message.Address, message.Port, string(message.Data))
	}
}
```

## Demos

Run both built-in demos:

```sh
./demo.sh
```

Run the two-peer localhost demo:

```sh
go run ./cmd/brk-demo
```

Run the lossy retry demo:

```sh
go run ./cmd/brk-lossy-demo
```

Two-terminal chat demo:

```sh
go run ./example -ip 127.0.0.1 -port 6000 127.0.0.1 6001
```

```sh
go run ./example -ip 127.0.0.1 -port 6001 127.0.0.1 6000
```

## RetryConfig

Start with the defaults, then override the retry settings you want:

```go
config := brk.DefaultRetryConfig()
config.QueueLength = 2000
config.RetryInterval = 5 * time.Second
config.AckTimeout = 2 * time.Second
config.MaxAttempts = 0
config.DuplicateTTL = 5 * time.Minute
```

- `QueueLength`: channel buffer size for incoming and outgoing messages.
- `RetryInterval`: how often cached messages are checked for retransmission.
- `AckTimeout`: how long a message can wait without acknowledgement before it is resent.
- `MaxAttempts`: retransmission limit. `0` means keep retrying until acknowledged.
- `DuplicateTTL`: how long received sequence numbers stay in the duplicate cache.

## Stats

```go
stats := server.Stats()
fmt.Printf("sent=%d received=%d acked=%d retried=%d duplicates=%d failed=%d dropped=%d\n", stats.Sent, stats.Received, stats.Acked, stats.Retried, stats.Duplicates, stats.FailedWrites, stats.Dropped)
```

- `Sent`: UDP packets written successfully.
- `Received`: UDP packets read successfully.
- `Acked`: acknowledgements received.
- `Retried`: cached application messages retransmitted.
- `Duplicates`: inbound duplicate application messages suppressed.
- `FailedWrites`: outbound UDP writes that failed.
- `Dropped`: cached messages removed after `MaxAttempts`.

## STUN

STUN is optional and never runs during server startup.

```go
config := brk.DefaultSTUNConfig()
address, err := brk.DiscoverExternalAddress(config)
if err != nil {
	panic(err)
}
fmt.Printf("%s:%d via %s\n", address.IP, address.Port, address.Server)
```

`STUNConfig` fields:

- `Server`: UDP STUN server address. Default: `stun.l.google.com:19302`.
- `Timeout`: connection, write, and read deadline. Default: `3s`.

## Commands

```sh
./build.sh
./test.sh
./run.sh
./demo.sh
./install.sh
```

## Release

Recommended first tag after merging this API set: `v0.1.0`.
