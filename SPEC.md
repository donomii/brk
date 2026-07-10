# brk SPEC

## Summary

`brk` is a Go library for sending discrete UDP messages. It can run as a plain UDP message router or as a retrying router that resends application messages until an acknowledgement arrives.

## Public Behavior

`StartUdpContext` starts a UDP listener on the requested local host and port. Port `0` asks the operating system for an available local port. The returned server exposes `Incoming`, `Outgoing`, `LocalAddress`, `Stats`, `Done`, and `Close`. Startup rejects a nil context or processor. Stopping the listener closes `Incoming` so processors ranging over it can finish. `Close` and `Done` wait for the owned network I/O loops, but not for the application processor.

`StartUdp` keeps the older blocking API. It starts the same UDP listener and blocks until the server stops.

`StartRetryUdpContext` starts a retrying UDP listener. Application code reads delivered messages from `Incoming` and sends messages through `Outgoing`. Each outgoing application message is assigned a process-wide sequence number, validated, cached, and transmitted as a JSON `UdpMessage`. The starting sequence is randomized on process startup. An `Ack` removes a cached message only when its sequence and source port match the pending target. For literal IP targets, the source IP must also match. Hostname targets cannot be pinned to the resolved IP by the current asynchronous writer. `Close` and `Done` wait for the owned retry and network I/O loops, but not for the application processor.

`StartRetryUdp` keeps the older blocking retry API. It uses `DefaultRetryConfig`.

`SendMessage` copies the supplied data, then puts a `UdpMessage` onto an outgoing channel with the copied data, target address, target port, a new sequence number, empty type, and current cache timestamp.

`DiscoverExternalAddress` sends one STUN binding request from a temporary UDP socket and returns the external address from a `XOR-MAPPED-ADDRESS` or `MAPPED-ADDRESS` response. The socket closes before return, so the result does not advertise an existing `UdpServer`. This helper is optional and is not called by server startup.

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
- `DuplicateTTL`: how long received source-endpoint and sequence combinations stay in the duplicate cache. Default: `5m`.

`STUNConfig`

- `Server`: UDP STUN server address. Default: `stun.l.google.com:19302`.
- `Timeout`: connection, write, and read deadline for the STUN exchange. Default: `3s`.

`DeliveryStatsSnapshot`

- `Sent`: UDP packets written successfully.
- `Received`: UDP packets read successfully.
- `Acked`: acknowledgement packets that matched and removed pending messages.
- `Retried`: application messages retransmitted.
- `Duplicates`: inbound duplicate application messages suppressed.
- `FailedWrites`: outbound messages rejected during validation or failed during a UDP write.
- `Dropped`: cached messages removed after `MaxAttempts`.

## Packet Format

Every packet is JSON encoded as `UdpMessage`.

The maximum encoded JSON packet size is 32,768 bytes. Larger outgoing messages fail before the network write. The receive path reads one extra byte so oversized datagrams are rejected explicitly instead of being decoded from truncated JSON.

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
2. Validate the target port and encoded packet size. Record a failed write and stop handling an invalid message.
3. Store a valid message in the retry cache with zero retransmission attempts.
4. Send the message to the network outgoing channel.

On every retry interval:

1. Read retry cache keys in sequence order.
2. For each cached message older than `AckTimeout`, check `MaxAttempts`.
3. If `MaxAttempts` is nonzero and the message has already reached it, remove the message and increment `Dropped`.
4. Otherwise update the cached timestamp, increment attempts, send the message again, and increment `Retried`.

When a non-acknowledgement message arrives:

1. Remove expired duplicate-cache entries.
2. Check the source address, source port, and sequence against the duplicate cache.
3. If the message is new, place it on the application queue and then send its acknowledgement.
4. If the message is a duplicate, send its acknowledgement immediately and increment `Duplicates`.

When an acknowledgement arrives:

1. Check that the sequence is pending and the acknowledgement source matches the cached target.
2. Remove the matching sequence from the retry cache.
3. Increment `Acked` only when a cached message was removed.

## Demos

`cmd/brk-demo` starts two retrying localhost servers, sends one message each way, prints the delivered data, and prints stats.

`cmd/brk-lossy-demo` starts a retrying sender and a raw receiver. The receiver skips the first acknowledgement, accepts the retry, then acknowledges it.

## Release Notes

First release tag to create after merging these changes: `v0.1.0`.

Use later `v0.x` tags for compatible improvements. Reserve `v1.0.0` for a stable packet/API contract.
