package brk

import (
	"log"
	"net/netip"
)

func logCloseFailure(address string, err error) {
	log.Printf("close UDP listener failed: address=%s error=%v", address, err)
}

func logUDPReadFailure(address string, err error) {
	log.Printf("read UDP message failed: local_address=%s expected=JSON_brk_UdpMessage error=%v", address, err)
}

func logUDPWriteFailure(message UdpMessage, err error) {
	log.Printf("write UDP message failed: sequence=%d target=%s:%d error=%v", message.Sequence, message.Address, message.Port, err)
}

func logRetransmitted(count int) {
	log.Printf("retransmitted UDP messages: count=%d", count)
}

func logDuplicate(message UdpMessage) {
	log.Printf("discarded duplicate UDP message: sequence=%d source=%s:%d", message.Sequence, message.Address, message.Port)
}

func logDropped(message UdpMessage, attempts int) {
	log.Printf("dropped UDP message after max attempts: sequence=%d target=%s:%d attempts=%d", message.Sequence, message.Address, message.Port, attempts)
}

func logAuthenticationFailure(message UdpMessage, err error) {
	log.Printf("authenticate UDP message failed: session_id=%s message_id=%s source=%s:%d error=%v", message.SessionID, message.MessageID, message.Address, message.Port, err)
}

func logInvalidRetryPacket(message UdpMessage, err error) {
	log.Printf("validate retry UDP packet failed: version=%d session_id=%s message_id=%s source=%s:%d error=%v", message.Version, message.SessionID, message.MessageID, message.Address, message.Port, err)
}

func logRejectedMessage(message UdpMessage, err error) {
	log.Printf("reject outgoing UDP message: session_id=%s message_id=%s target=%s:%d error=%v", message.SessionID, message.MessageID, message.Address, message.Port, err)
}

func logSTUNPacketFailure(source netip.AddrPort, err error) {
	log.Printf("handle STUN packet failed: source=%v error=%v", source, err)
}

func logControlWriteFailure(operation string, destination netip.AddrPort, err error) {
	log.Printf("write UDP control packet failed: operation=%s destination=%v error=%v", operation, destination, err)
}
