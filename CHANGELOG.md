# Changelog

## Unreleased

- Added the opt-in `ProtocolV2` binary wire format: a 44-byte header with raw payload and a 32-byte HMAC trailer, detected automatically on receive alongside version 1 and legacy JSON.
- Added the `Logf` package variable so applications can route brk diagnostics into their own logger.
- Replaced per-message duplicate-cache scans with an expiry-ordered queue, making inbound bookkeeping proportional to expired entries instead of all cached entries.
- Recycled receive buffers through a pool to cut per-datagram allocation.
- Added STUN parser coverage for XOR-mapped IPv6 addresses and odd-length attribute skipping, and a socket-level duplicate-suppression test.
- Moved the two-terminal chat example to `examples/chat`.
- Added macOS runners and a format gate to CI, and a tag-driven release workflow.
- Removed the unused GitHub Pages configuration.

## v0.1.0 - 2026-07-10

- Added terminal delivery receipts, deadlines, bounded pending queues, exponential backoff, deterministic jitter, and precise write results.
- Added the compact version 1 wire protocol with random session/message IDs and optional HMAC-SHA256 authentication.
- Added IPv4/IPv6 `netip.AddrPort` APIs, live-socket STUN discovery, peer hole punching, and keepalives.
- Hardened acknowledgement identity, duplicate suppression, retry races, packet-size enforcement, payload ownership, and server shutdown.
- Added deterministic local coverage for delivery, authentication, IPv6, STUN, punching, keepalives, lifecycle, packet boundaries, and retry races.
- Added localhost, lossy-link, and NAT traversal demos with no-configuration launchers.
- Added contributor guidance, security reporting instructions, issue forms, release automation, and Go-version CI coverage.
