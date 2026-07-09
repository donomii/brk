# brk SPEC

## Summary

`brk` is a Go library for sending discrete UDP messages. It can run as a plain UDP message router or as a retrying router that resends application messages until an acknowledgement arrives.

## Public Behavior

`StartUdpContext` starts a UDP listener on the requested local host and port. Port `0` asks the operating system for an available local port. The returned server exposes `Incoming`, `Outgoing`, `LocalAddress`, `Stats`, `Done`, and `Close`.

`StartUdp` keeps the older blocking API. It starts the same UDP listener and blocks until the server stops.

`StartRetryUdpContext` starts a retrying UDP listener. Application code reads delivered messages from `Incoming` and sends messages through `Outgoing`. Each outgoing application message is assigned a sequence number, cached, and transmitted as a JSON `UdpMessage`. When an `Ack` message with the same sequence number arrives, the cached message is removed.

`StartRetryUdp` keeps the older blocking retry API. It uses `DefaultRetryConfig`.

`SendMessage` puts a `UdpMessage` onto an outgoing channel with the supplied data, target address, target port, a new sequence number, empty type, and current cache timestamp.

`DiscoverExternalAddress` sends one STUN binding request to the configured STUN server and returns the external address from a `XOR-MAPPED-ADDRESS` or `MAPPED-ADDRESS` response. This helper is optional and is not called by server startup.

## Public Types

`UdpMessage`

- `Data`: bytes delivered to the receiver.
- `Address`: target address for outgoing messages; source address for incoming messages.
- `Port`: target port for outgoing messages; source port for incoming messages.
- `Sequence`: message sequence used for retry acknowledgements.
- `Type`: empty for application messages and `Ack` for acknowledgement messages.
- `Cached`: timestamp used by retry logic.

`RetryConfig`

- `QueueLength`: channel buffer size for incoming and outgoing queues. Default: `2000`.
- `RetryInterval`: how often the retry cache is scanned. Default: `5s`.
- `AckTimeout`: message age required before retransmission. Default: `2s`.
- `MaxAttempts`: maximum retransmission attempts. Default: `0`, meaning retry until acknowledged.
- `DuplicateTTL`: how long received sequence numbers stay in the duplicate cache. Default: `5m`.

`STUNConfig`

- `Server`: UDP STUN server address. Default: `stun.l.google.com:19302`.
- `Timeout`: connection, write, and read deadline for the STUN exchange. Default: `3s`.

`DeliveryStatsSnapshot`

- `Sent`: UDP packets written successfully.
- `Received`: UDP packets read successfully.
- `Acked`: acknowledgement packets received.
- `Retried`: application messages retransmitted.
- `Duplicates`: inbound duplicate application messages suppressed.
- `FailedWrites`: outbound UDP writes that failed.
- `Dropped`: cached messages removed after `MaxAttempts`.

## Packet Format

Every packet is JSON encoded as `UdpMessage`.

Application message:

```json
{"Data":"base64 bytes","Address":"target or source","Port":1234,"Sequence":3,"Type":"","Cached":"timestamp"}
```

Acknowledgement message:

```json
{"Data":"","Address":"target or source","Port":1234,"Sequence":3,"Type":"Ack","Cached":"timestamp"}
```

Receivers replace `Address` and `Port` with the actual UDP source address before delivering the message to application code.

## Retry Algorithm

When application code queues an outgoing message:

1. Assign a fresh sequence number.
2. Store the message in the retry cache with zero retransmission attempts.
3. Send the message to the network outgoing channel.

On every retry interval:

1. Read retry cache keys in sequence order.
2. For each cached message older than `AckTimeout`, check `MaxAttempts`.
3. If `MaxAttempts` is nonzero and the message has already reached it, remove the message and increment `Dropped`.
4. Otherwise update the cached timestamp, increment attempts, send the message again, and increment `Retried`.

When a non-acknowledgement message arrives:

1. Send an acknowledgement before duplicate filtering.
2. Remove expired duplicate-cache entries.
3. Deliver the message only if its sequence number is not still in the duplicate cache.
4. Record duplicate suppression in `Duplicates`.

When an acknowledgement arrives:

1. Remove the matching sequence from the retry cache.
2. Increment `Acked`.

## Demos

`cmd/brk-demo` starts two retrying localhost servers, sends one message each way, prints the delivered data, and prints stats.

`cmd/brk-lossy-demo` starts a retrying sender and a raw receiver. The receiver skips the first acknowledgement, accepts the retry, then acknowledges it.

## Release Notes

First release tag to create after merging these changes: `v0.1.0`.

Use later `v0.x` tags for compatible improvements. Reserve `v1.0.0` for a stable packet/API contract.
