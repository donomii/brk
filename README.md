[![Go](https://github.com/donomii/brk/actions/workflows/test.yml/badge.svg)](https://github.com/donomii/brk/actions/workflows/test.yml)
[![Codecov](https://codecov.io/gh/donomii/brk/branch/master/graph/badge.svg)](https://app.codecov.io/github/donomii/brk)
[![pkg.go.dev](https://pkg.go.dev/badge/github.com/donomii/brk.svg)](https://pkg.go.dev/github.com/donomii/brk)

# brk

Message-level reliable UDP for Go.

`brk` sends byte messages over UDP without turning them into a stream. It has no external dependencies.

- Delivery acknowledgements, receipts, duplicate suppression, deadlines, and bounded pending queues.
- Exponential retry backoff with deterministic jitter and configurable limits.
- Versioned packets with random session/message IDs and optional HMAC-SHA256 authentication.
- IPv4 and IPv6 endpoints through `netip.AddrPort`.
- STUN discovery through the live server socket, peer hole punching, and keepalives.

## Install

```sh
go get github.com/donomii/brk
```

## Quickstart

```sh
go run ./cmd/brk-demo
```

The command starts two retrying localhost peers on automatically selected ports and sends one message in each direction:

```text
A received: hello from B
B received: hello from A
A stats: {Sent:2 Received:2 Acked:1 Retried:0 Duplicates:0 FailedWrites:0 Dropped:0 Expired:0 Rejected:0 AuthenticationFailures:0 InvalidPackets:0}
B stats: {Sent:2 Received:2 Acked:1 Retried:0 Duplicates:0 FailedWrites:0 Dropped:0 Expired:0 Rejected:0 AuthenticationFailures:0 InvalidPackets:0}
```

## Demos

Run all built-in demos:

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

Run the local hole-punch and keepalive demo:

```sh
./nat-demo.sh
```

Two-terminal chat demo:

```sh
go run ./example -ip 127.0.0.1 -port 6000 127.0.0.1 6001
```

```sh
go run ./example -ip 127.0.0.1 -port 6001 127.0.0.1 6000
```

## Delivery receipts

`Send` accepts a validated `netip.AddrPort`, returns the message ID immediately, and provides one terminal result:

```go
delivery, err := server.Send(ctx, brk.SendRequest{
	Data:   []byte("hello"),
	Target: peer,
})
if err != nil {
	panic(err)
}

result, err := delivery.Wait(ctx)
if err != nil {
	panic(err)
}
fmt.Printf("id=%s status=%s attempts=%d latency=%v error=%s\n", delivery.ID(), result.Status, result.Attempts, result.Latency, result.WriteError)
```

Terminal statuses are `acknowledged`, `dropped`, `failed`, `expired`, `rejected`, and `canceled`. `Reason` distinguishes acknowledgements, deadlines, maximum attempts, write failures, pending limits, invalid messages, and server shutdown.

The older `Outgoing` channel and `SendMessage` remain available when a receipt is not needed.

## RetryConfig

Start with the defaults, then override the retry settings you want:

```go
config := brk.DefaultRetryConfig()
config.QueueLength = 2000
config.RetryInterval = 5 * time.Second
config.AckTimeout = 2 * time.Second
config.MaxAttempts = 0
config.DuplicateTTL = 5 * time.Minute
config.MaxPending = 2000
config.BackoffMultiplier = 2
config.MaxRetryDelay = 30 * time.Second
config.JitterFraction = 0.20
config.DisableJitter = false
config.DeliveryTimeout = time.Minute
config.DisableDeliveryTimeout = false
config.AuthenticationKey = nil
```

- `QueueLength`: channel buffer size for incoming and outgoing messages.
- `RetryInterval`: how often pending messages are checked. Default: `5s`.
- `AckTimeout`: initial delay after a completed write before retrying. Default: `2s`.
- `MaxAttempts`: maximum retransmissions after the initial write. Default: `0`, meaning no attempt limit.
- `DuplicateTTL`: how long source/session/message identities remain in the duplicate cache. Default: `5m`.
- `MaxPending`: maximum messages awaiting terminal results. Default: `2000`.
- `BackoffMultiplier`: retry-delay multiplier, at least `1`. Default: `2`.
- `MaxRetryDelay`: upper bound for exponential delay. Default: `30s`.
- `JitterFraction`: deterministic variation around each retry delay in `[0,1)`. Default: `0.20`.
- `DisableJitter`: set to `true` for exact backoff delays. Default: `false`.
- `DeliveryTimeout`: default per-message deadline. Default: `1m`.
- `DisableDeliveryTimeout`: set to `true` for no default deadline. Default: `false`.
- `AuthenticationKey`: optional shared HMAC-SHA256 key. Empty disables authentication; configured keys require at least 32 bytes.

With an authentication key, unsigned packets, legacy packets, altered packets, and packets signed with a different key are rejected. Authentication protects integrity and peer possession of the shared key; it does not encrypt payloads.

## Stats

```go
stats := server.Stats()
fmt.Printf("sent=%d received=%d acked=%d retried=%d duplicates=%d failed=%d dropped=%d expired=%d rejected=%d auth_failures=%d invalid=%d\n", stats.Sent, stats.Received, stats.Acked, stats.Retried, stats.Duplicates, stats.FailedWrites, stats.Dropped, stats.Expired, stats.Rejected, stats.AuthenticationFailures, stats.InvalidPackets)
```

- `Sent`: UDP packets written successfully.
- `Received`: UDP packets read successfully.
- `Acked`: acknowledgements that matched and removed pending messages.
- `Retried`: cached application messages retransmitted.
- `Duplicates`: inbound duplicate application messages suppressed.
- `FailedWrites`: outbound UDP writes that failed.
- `Dropped`: messages terminally removed after their attempt limit or a final write failure.
- `Expired`: messages removed at their deadline.
- `Rejected`: messages rejected before queueing, including pending-limit and packet-validation failures.
- `AuthenticationFailures`: inbound packets rejected by authentication.
- `InvalidPackets`: inbound retry packets rejected by protocol validation.

## STUN

STUN is optional and never runs during server startup. The server method uses the live UDP socket, so the returned mapping belongs to that server:

```go
config := brk.DefaultSTUNConfig()
address, err := server.DiscoverExternalAddress(ctx, config)
if err != nil {
	panic(err)
}
fmt.Printf("%s:%d via %s\n", address.IP, address.Port, address.Server)
```

`STUNConfig` fields:

- `Server`: UDP STUN server address. Default: `stun.l.google.com:19302`.
- `Timeout`: connection, write, and read deadline. Default: `3s`.

The package-level `brk.DiscoverExternalAddress(config)` remains for compatibility, but it probes a temporary socket that closes before return.

## Hole punching and keepalives

After peers exchange candidates through an out-of-band rendezvous service, each can punch the other candidate from its live socket:

```go
result, err := server.PunchPeer(ctx, peer, brk.DefaultPunchConfig())
if err != nil {
	panic(err)
}
fmt.Printf("peer=%v observed=%v attempts=%d round_trip=%v\n", result.Peer, result.ObservedAddress, result.Attempts, result.RoundTrip)
```

`PunchConfig.Attempts` defaults to `8`; `Interval` defaults to `250ms`. Both must be positive.

Keep the established UDP mapping active with a caller-owned blocking loop:

```go
err = server.KeepPeerAlive(ctx, peer, brk.DefaultKeepaliveConfig())
```

`KeepaliveConfig.Interval` defaults to `20s`. Cancel `ctx` to stop the loop.

## IPv6

Bind an IPv6 server with an explicit IPv6 literal such as `::1` or `::`. `LocalEndpoint`, `Send`, `SendMessageTo`, live STUN, punching, and keepalives use normalized `netip.AddrPort` values. One server owns one address-family socket; run separate IPv4 and IPv6 servers when both families are required.

## Limitations

- Messages are not ordered or persisted.
- The library does not provide congestion control or payload encryption.
- Encoded packets may not exceed 32,768 bytes; packets larger than the network path MTU may be fragmented.
- Hole punching requires out-of-band candidate exchange, does not traverse every symmetric NAT, and does not replace TURN relaying.
- Shared-key authentication does not authenticate unauthenticated STUN control packets.

## Commands

```sh
./build.sh
./test.sh
./run.sh
./demo.sh
./nat-demo.sh
./install.sh
```

## Release

See [`RELEASE_NOTES.md`](RELEASE_NOTES.md) for v0.1.0 highlights and compatibility notes.
