# v0.1.0 — Reliable message-level UDP for Go

`brk` keeps UDP messages discrete while adding acknowledgement-based delivery, useful terminal receipts, and NAT traversal tools. It uses only the Go standard library.

## Highlights

- Terminal delivery receipts with deadlines, bounded pending queues, exact write errors, and attempt counts.
- Exponential retry backoff with deterministic jitter and configurable limits.
- Compact version 1 packets with random session/message IDs and optional HMAC-SHA256 authentication.
- IPv4 and IPv6 endpoints through `netip.AddrPort`.
- Live-socket STUN discovery, peer hole punching, and keepalives.
- Localhost, lossy-link, and NAT traversal demos with no-configuration launchers.

## Compatibility

The original version 0 JSON packet format still decodes. Retrying sends now use version 1. Configured authentication keys must contain at least 32 bytes; authenticated retry servers reject unsigned and legacy packets.

See `README.md` for the quickstart and `SPEC.md` for the complete wire and retry contracts.
