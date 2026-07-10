# Changelog

## v0.1.0 - 2026-07-10

- Added terminal delivery receipts, deadlines, bounded pending queues, exponential backoff, deterministic jitter, and precise write results.
- Added the compact version 1 wire protocol with random session/message IDs and optional HMAC-SHA256 authentication.
- Added IPv4/IPv6 `netip.AddrPort` APIs, live-socket STUN discovery, peer hole punching, and keepalives.
- Hardened acknowledgement identity, duplicate suppression, retry races, packet-size enforcement, payload ownership, and server shutdown.
- Added deterministic local coverage for delivery, authentication, IPv6, STUN, punching, keepalives, lifecycle, packet boundaries, and retry races.
- Added localhost, lossy-link, and NAT traversal demos with no-configuration launchers.
- Added contributor guidance, security reporting instructions, issue forms, release automation, and Go-version CI coverage.
