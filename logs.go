package brk

import (
	"log"
	"net/netip"
)

// Logf receives every diagnostic log line the package emits. Replace it to
// route diagnostics into your own logger; the replacement must be safe for
// concurrent use. The default writes through the standard log package.
var Logf func(format string, values ...any) = log.Printf

func logCloseFailure(address string, err error) {
	Logf("close UDP listener failed: address=%s error=%v", address, err)
}

func logUDPReadFailure(address string, err error) {
	Logf("read UDP message failed: local_address=%s expected=JSON_brk_UdpMessage error=%v", address, err)
}

func logUDPWriteFailure(message UdpMessage, err error) {
	Logf("write UDP message failed: sequence=%d target=%s:%d error=%v", message.Sequence, message.Address, message.Port, err)
}

func logRetransmitted(count int) {
	Logf("retransmitted UDP messages: count=%d", count)
}

func logDuplicate(message UdpMessage) {
	Logf("discarded duplicate UDP message: sequence=%d source=%s:%d", message.Sequence, message.Address, message.Port)
}

func logDropped(message UdpMessage, attempts int) {
	Logf("dropped UDP message after max attempts: sequence=%d target=%s:%d attempts=%d", message.Sequence, message.Address, message.Port, attempts)
}

func logAuthenticationFailure(message UdpMessage, err error) {
	Logf("authenticate UDP message failed: session_id=%s message_id=%s source=%s:%d error=%v", message.SessionID, message.MessageID, message.Address, message.Port, err)
}

func logInvalidRetryPacket(message UdpMessage, err error) {
	Logf("validate retry UDP packet failed: version=%d session_id=%s message_id=%s source=%s:%d error=%v", message.Version, message.SessionID, message.MessageID, message.Address, message.Port, err)
}

func logRejectedMessage(message UdpMessage, err error) {
	Logf("reject outgoing UDP message: session_id=%s message_id=%s target=%s:%d error=%v", message.SessionID, message.MessageID, message.Address, message.Port, err)
}

func logOrderingReleases(count int) {
	Logf("released held UDP messages after ordering gap timeout: count=%d", count)
}

func logReassemblyFailure(message UdpMessage, err error) {
	Logf("reassemble UDP fragment failed: session_id=%s group=%s index=%d count=%d source=%s:%d error=%v", message.SessionID, message.FragmentGroup, message.FragmentIndex, message.FragmentCount, message.Address, message.Port, err)
}

func logReassemblyEvictions(message UdpMessage, count, groups, bytes int) {
	Logf("evicted incomplete UDP fragment groups for reassembly capacity: evicted=%d retained_groups=%d retained_bytes=%d incoming_group=%s source=%s:%d", count, groups, bytes, message.FragmentGroup, message.Address, message.Port)
}

func logOrderingCapacityReleases(count, held, perPeerLimit, totalLimit int) {
	Logf("released ordered UDP messages for hold capacity: released=%d retained=%d per_peer_limit=%d total_limit=%d", count, held, perPeerLimit, totalLimit)
}

func logSTUNPacketFailure(source netip.AddrPort, err error) {
	Logf("handle STUN packet failed: source=%v error=%v", source, err)
}

func logControlWriteFailure(operation string, destination netip.AddrPort, err error) {
	Logf("write UDP control packet failed: operation=%s destination=%v error=%v", operation, destination, err)
}
