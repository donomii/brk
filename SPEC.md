# brk SPEC

## Summary

`brk` is a standard-library-only Go package for discrete UDP messages. It provides plain routing, acknowledged retry delivery, receipts, optional shared-key authentication, IPv4/IPv6 endpoints, live-socket STUN discovery, hole punching, and keepalives.

## Public Behavior

`StartUdpContext` starts a UDP listener on the requested local host and port. Port `0` asks the operating system for an available local port. IPv4 remains the default; an explicit IPv6 literal selects IPv6. The returned server exposes `Incoming`, `Outgoing`, `LocalAddress`, `LocalEndpoint`, `WriteResults`, `Stats`, `Done`, and `Close`. Startup rejects a nil context or processor. Stopping the listener closes `Incoming`. `Close` and `Done` wait for owned network I/O loops, but not for the application processor.

`StartUdp` keeps the older blocking API. It starts the same UDP listener and blocks until the server stops.

`StartRetryUdpContext` starts a retrying UDP listener with a random 128-bit session ID. Application code reads delivered messages from `Incoming`, keeps using `Outgoing` for compatibility, or calls `Send` for a delivery handle. Every retry message receives a random 128-bit message ID and a diagnostic sequence. Outgoing packets use the format selected by `RetryConfig.WireVersion`. `Close` and `Done` wait for owned retry and network I/O loops, complete pending delivery handles as canceled, and do not wait for the application processor.

`StartRetryUdp` keeps the older blocking retry API. It uses `DefaultRetryConfig`.

`SendMessage` copies the supplied data, then puts a `UdpMessage` onto an outgoing channel with the copied data, target address, target port, a new sequence number, empty type, and current cache timestamp.

`SendMessageTo` validates a `netip.AddrPort` and queues a copied payload. `RetryUdpServer.Send` accepts a `SendRequest`, queues a version-1 message, and returns a `Delivery`. `Delivery.ID` returns the message ID. `Wait`, `Done`, and `Result` expose exactly one terminal `DeliveryResult`.

The package function `DiscoverExternalAddress` uses a temporary UDP socket. `UdpServer.DiscoverExternalAddress` and the retry-server forwarding method use the server's live socket so the returned mapping belongs to the running server. STUN is optional and doesn't run during startup.

`PunchPeer` sends marked STUN binding requests from the live socket until a peer replies or the configured attempts end. `KeepPeerAlive` immediately sends a marked STUN binding indication and repeats at the configured interval until its context or server ends.

Diagnostic log lines write through the package variable `Logf`, which defaults to `log.Printf`. Applications may replace it with any concurrency-safe formatter.

## Public Types

`UdpMessage`

- `Data`: bytes delivered to the receiver.
- `Address`: target address for outgoing messages; source address for incoming messages.
- `Port`: target port for outgoing messages; source port for incoming messages.
- `Sequence`: message sequence used for retry acknowledgements.
- `Type`: empty for application messages and `Ack` for acknowledgement messages.
- `Cached`: timestamp used by retry logic.
- `Version`: `0` for legacy packets and `1` for the versioned wire format.
- `SessionID`: 32 lowercase hexadecimal characters identifying the sending retry server.
- `MessageID`: 32 lowercase hexadecimal characters identifying the message.
- `Deadline`: sender-local terminal deadline; it is not transmitted.
- `Signature`: unpadded base64url HMAC for version-1 packets; it is not delivered as application data.

`RetryConfig`

- `QueueLength`: channel buffer size for incoming and outgoing queues. Default: `2000`.
- `RetryInterval`: how often the retry cache is scanned. Default: `5s`.
- `AckTimeout`: message age required before retransmission. Default: `2s`.
- `MaxAttempts`: maximum retransmission attempts. Default: `0`, meaning retry until acknowledged.
- `DuplicateTTL`: how long received source-endpoint and sequence combinations stay in the duplicate cache. Default: `5m`.
- `MaxPending`: maximum pending messages. Default: `2000`.
- `BackoffMultiplier`: exponential retry multiplier, at least `1`. Default: `2`.
- `MaxRetryDelay`: upper bound for retry delay. Default: `30s`.
- `JitterFraction`: deterministic variation in `[0,1)`. Default: `0.20`.
- `DisableJitter`: makes retry delays exact when true. Default: `false`.
- `DeliveryTimeout`: default sender-local deadline. Default: `1m`.
- `DisableDeliveryTimeout`: removes the default deadline when true. Default: `false`.
- `AuthenticationKey`: optional shared HMAC-SHA256 key. Empty disables authentication; configured keys contain at least 32 bytes.
- `WireVersion`: outgoing packet format, `1` (JSON) or `2` (binary). Default: `1`. Inbound packets of every version are always accepted.

`SendRequest`

- `Data`: payload copied before queueing.
- `Target`: validated, normalized `netip.AddrPort` in the same address family as the server.
- `Deadline`: optional absolute deadline. Zero uses `RetryConfig.DeliveryTimeout`.

`DeliveryResult`

- `SessionID`, `MessageID`, and `Target`: delivery identity and target.
- `Status`: `acknowledged`, `dropped`, `failed`, `expired`, `rejected`, or `canceled`.
- `Reason`: `acknowledgement`, `deadline`, `max_attempts`, `write_failure`, `pending_limit`, `invalid_message`, or `server_shutdown`.
- `Attempts`: dispatched UDP writes including the initial write.
- `Latency`: terminal time minus accepted time.
- `WriteError`: latest exact resolution or write error, including an earlier error followed by a later successful write.

`PunchConfig` defaults to 8 attempts and a `250ms` interval. `PunchResult` reports the peer, the address it observed, successful attempt number, and round-trip time.

`KeepaliveConfig.Interval` defaults to `20s`.

`STUNConfig`

- `Server`: UDP STUN server address. Default: `stun.l.google.com:19302`.
- `Timeout`: connection, write, and read deadline for the STUN exchange. Default: `3s`.

`DeliveryStatsSnapshot`

- `Sent`: UDP packets written successfully.
- `Received`: UDP packets read successfully.
- `Acked`: acknowledgement packets that matched and removed pending messages.
- `Retried`: application messages retransmitted.
- `Duplicates`: inbound duplicate application messages suppressed.
- `FailedWrites`: application UDP writes that failed.
- `Dropped`: messages removed after their attempt limit or final failed write.
- `Expired`: messages removed at their deadline.
- `Rejected`: messages rejected before entering the pending cache.
- `AuthenticationFailures`: inbound retry packets rejected by HMAC policy or verification.
- `InvalidPackets`: inbound retry packets rejected by protocol validation.

## Packet Format

Encoded packets contain at most 32,768 bytes. The receive path reads one extra byte and rejects oversized datagrams before JSON decoding.

Legacy version 0 retains the original `UdpMessage` JSON object:

```json
{"Data":"base64 bytes","Address":"target or source","Port":1234,"Sequence":3,"Type":"","Cached":"timestamp"}
```

Version 1 data packet:

```json
{"v":1,"kind":"data","sid":"32 lowercase hex characters","mid":"32 lowercase hex characters","seq":3,"data":"base64 bytes","auth":"optional unpadded base64url HMAC"}
```

Version 1 acknowledgement:

```json
{"v":1,"kind":"ack","sid":"original sender session","mid":"original message ID","seq":3,"auth":"optional unpadded base64url HMAC"}
```

Version 1 decoding rejects unknown fields, trailing JSON, unsupported versions, malformed IDs, unknown kinds, and acknowledgement payloads. `Address`, `Port`, `Cached`, and `Deadline` are not transmitted in version 1. The receiver fills `Address` and `Port` from the normalized UDP source endpoint.

Version 2 data and acknowledgement packets are binary and big-endian: magic byte `0xB2`, version byte `2`, kind byte (`1` data, `2` ack), flags byte (bit 0 marks a signature trailer), 16 raw session ID bytes, 16 raw message ID bytes, an unsigned 64-bit sequence, and the payload running to the end of the packet, followed by a 32-byte HMAC-SHA256 trailer when the signed flag is set. The header is 44 bytes. Decoding rejects short headers, other version bytes, unknown flags, unknown kinds, acknowledgement payloads, sequences that overflow the platform integer, and signed packets shorter than the trailer. The magic byte cannot collide with JSON packets, which start with `{`, or STUN packets, whose first two bits are zero. Receivers detect each packet's format, and acknowledgements answer in the format of the message they acknowledge.

When authentication is enabled, HMAC-SHA256 covers the compact fixed-order version, kind, session ID, message ID, sequence, and data envelope. The envelope embeds the packet's protocol version, so a signature never validates across formats. Version 1 carries the HMAC in `auth`; version 2 carries it as the binary trailer. Empty keys send and accept only unsigned packets. Configured keys reject unsigned packets and every legacy packet. Servers without a key reject signed packets. Comparison is constant-time. Keys never appear in errors or logs.

## Retry Algorithm

When application code queues an outgoing message:

1. Copy payload data and assign version, session ID, message ID, sequence, accepted time, and deadline.
2. Validate deadline, target, IDs, authentication, encoded size, and pending capacity.
3. Reject invalid or over-capacity messages with one terminal receipt and increment `Rejected`.
4. Store a valid message atomically with attempt count 1 and write-in-flight state.
5. Dispatch the initial write through the network outgoing queue.

The UDP writer returns a `WriteResult` containing the session ID, message ID, sequence, normalized target, completion time, exact error, and whether the error is permanent. A successful or retriable failed write clears in-flight state and schedules one retry time. A permanent error, or a failed final attempt, removes the entry and completes it as `failed/write_failure`.

After completed attempt count `attempts`, retry delay is:

```text
base delay = min(MaxRetryDelay, AckTimeout × BackoffMultiplier^(attempts-1))
jitter factor = 1 - JitterFraction + 2 × JitterFraction × deterministic fraction(message ID, attempts)
retry delay = min(MaxRetryDelay, base delay × jitter factor)
```

The jitter fraction is derived once from SHA-256 of the message ID and attempt count, so repeated cache scans cannot change a scheduled time. `DisableJitter` makes the factor `1`.

On every retry interval:

1. Read pending keys in sequence order.
2. Expire entries at their deadline.
3. Ignore entries with a write in flight or a future retry time.
4. Remove entries that exhausted `1 + MaxAttempts` dispatched writes. A final successful write becomes `dropped/max_attempts`; a final failed write becomes `failed/write_failure`.
5. Otherwise increment attempts, mark the write in flight, dispatch it, and increment `Retried`.

When a non-acknowledgement message arrives:

1. Remove expired duplicate-cache entries.
2. Check source address, source port, sequence, session ID, and message ID against the duplicate cache.
3. If the message is new, place it on the application queue and then send its acknowledgement.
4. If the message is a duplicate, send its acknowledgement immediately and increment `Duplicates`.

When an acknowledgement arrives:

1. Check that sequence, version, session ID, message ID, and source endpoint match the pending target.
2. Remove the matching sequence from the retry cache.
3. Complete the delivery once as `acknowledged/acknowledgement` and increment `Acked`.

Late acknowledgements and write results after terminal removal are ignored. Server shutdown removes all remaining entries and completes their handles as `canceled/server_shutdown`.

## Endpoints and NAT Control

Endpoints are normalized with IPv4-mapped IPv6 addresses converted to IPv4. `UdpMessage.Endpoint`, `ExternalAddress.Endpoint`, `LocalEndpoint`, `SendMessageTo`, and `Send` use `netip.AddrPort`. A server bound to an explicit IPv6 literal uses one IPv6 socket; every other existing host form retains IPv4 behavior. Destination and local address families must match.

The UDP reader is the only socket reader. It classifies STUN by message-type high bits and the `0x2112A442` cookie before JSON decoding. STUN length must be divisible by four and exactly match the datagram. STUN traffic does not enter application queues or application delivery counters.

Live STUN discovery serializes exchanges per server, resolves a family-compatible STUN endpoint in the caller's control path, writes from the live socket, and accepts only a response with the requested transaction and source endpoint. Caller cancellation, timeout, or server closure ends the exchange.

Hole punching sends binding requests carrying a `SOFTWARE=brk` attribute. A running server responds only to marked requests with a binding success containing the observed source as `XOR-MAPPED-ADDRESS`. `PunchPeer` repeats with a new transaction per attempt and returns the first valid result. Both peers exchange candidates out of band. Symmetric NAT and relay traversal are not guaranteed.

Keepalives are marked STUN binding indications. `KeepPeerAlive` sends immediately, repeats at `KeepaliveConfig.Interval`, solicits no response, and returns when its context or server ends.

## Demos

`cmd/brk-demo` starts two retrying localhost servers, sends one message each way, prints the delivered data, and prints stats.

`cmd/brk-lossy-demo` starts a retrying sender and a raw receiver. The receiver skips the first acknowledgement, accepts the retry, then acknowledges it.

`cmd/brk-nat-demo` starts two localhost servers, punches one peer, runs a short keepalive loop, and prints endpoints and timing. `nat-demo.sh` launches it without flags.

## Expected Files

- `build.sh`, `run.sh`, `test.sh`, `demo.sh`, `nat-demo.sh`, and `install.sh`: no-configuration launchers.
- `cmd/brk-demo`, `cmd/brk-lossy-demo`, and `cmd/brk-nat-demo`: runnable demonstrations.
- `README.md`: installation, quickstart, API configuration, NAT behavior, and limitations.
- `CHANGELOG.md`: tagged release behavior.
- `.github/workflows/test.yml`: Go-version matrix, vet, tests, race tests, coverage generation, and upload.
- `CONTRIBUTING.md`, `SECURITY.md`, issue templates, and pull-request template: repository contribution and reporting behavior.

## Release Notes

The first stable project snapshot is tagged `v0.1.0` and described in `RELEASE_NOTES.md`.

Use later `v0.x` tags for compatible improvements. Reserve `v1.0.0` for a stable packet/API contract.
