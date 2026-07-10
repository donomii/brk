// Package brk sends discrete UDP messages and can retry them until acknowledgements arrive.
//
// StartUdpContext provides plain UDP message routing. StartRetryUdpContext adds acknowledgements, retransmission,
// delivery statistics, and duplicate suppression scoped to each UDP peer. SendMessage copies its payload before
// queueing it, so callers may safely reuse their input bytes after it returns.
//
// brk does not provide message ordering, durable queues, congestion control, encryption, or peer authentication.
// DiscoverExternalAddress probes the mapping of a temporary socket rather than an existing UdpServer.
package brk
