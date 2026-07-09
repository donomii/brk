package brk

import "log"

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
