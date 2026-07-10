// Package brk sends discrete UDP messages and can retry them until acknowledgements arrive.
//
// StartUdpContext provides plain UDP message routing. StartRetryUdpContext adds acknowledgements, retransmission,
// terminal delivery receipts, deadlines, bounded queues, optional HMAC-SHA256 authentication, delivery statistics,
// and duplicate suppression scoped to each UDP peer. IPv4 and IPv6 endpoints are represented with netip.AddrPort.
// SendMessage copies its payload before queueing it, so callers may safely reuse their input bytes after it returns.
//
// Live servers can discover their external address with STUN, punch a peer endpoint, and send mapping keepalives.
// The package-level DiscoverExternalAddress function remains available for probing with a temporary socket.
//
// brk does not provide message ordering, durable queues, congestion control, payload encryption, rendezvous, or TURN
// relaying. Shared-key authentication protects packet integrity but does not encrypt payloads.
package brk
