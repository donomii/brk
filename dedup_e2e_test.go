package brk

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestRetryServerSuppressesWireDuplicatesButNotOtherPeers proves the
// duplicate cache end to end over real sockets: an identical resent packet is
// suppressed yet still acknowledged, while a different peer reusing the same
// sequence number is delivered.
func TestRetryServerSuppressesWireDuplicatesButNotOtherPeers(t *testing.T) {
	delivered := make(chan UdpMessage, 4)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, DuplicateTTL: time.Minute}, func(incoming, outgoing chan UdpMessage) {
		for message := range incoming {
			delivered <- message
		}
	})
	if err != nil {
		t.Fatalf("start retry receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)

	host, port := splitHostPort(t, receiver.LocalAddress())
	peerA := listenLocalUDP(t)
	defer closeUDPConn(t, peerA)
	peerB := listenLocalUDP(t)
	defer closeUDPConn(t, peerB)

	message := UdpMessage{Data: []byte("from peer A"), Address: host, Port: port, Sequence: 42, Cached: time.Now()}
	writeTestMessage(t, peerA, message)
	first := receiveMessage(t, delivered)
	if string(first.Data) != "from peer A" {
		t.Fatalf("first delivery mismatch: expected %q, received %q", "from peer A", string(first.Data))
	}
	expectAck(t, peerA, 42)

	writeTestMessage(t, peerA, message)
	expectAck(t, peerA, 42)
	waitForStats(t, receiver, func(snapshot DeliveryStatsSnapshot) bool {
		return snapshot.Duplicates >= 1
	})

	writeTestMessage(t, peerB, UdpMessage{Data: []byte("from peer B"), Address: host, Port: port, Sequence: 42, Cached: time.Now()})
	second := receiveMessage(t, delivered)
	if string(second.Data) != "from peer B" {
		t.Fatalf("cross-peer delivery mismatch: expected %q, received %q", "from peer B", string(second.Data))
	}
	expectAck(t, peerB, 42)

	select {
	case extra := <-delivered:
		t.Fatalf("duplicate delivery mismatch: expected no third delivery, received %q", string(extra.Data))
	default:
	}
}

func writeTestMessage(t *testing.T, conn *net.UDPConn, message UdpMessage) {
	t.Helper()
	err := writeUDPMessage(conn, message)
	if err != nil {
		t.Fatalf("write UDP message failed: expected nil error, received %v", err)
	}
}

func expectAck(t *testing.T, conn *net.UDPConn, sequence int) {
	t.Helper()
	err := conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err != nil {
		t.Fatalf("set UDP read deadline failed: expected nil error, received %v", err)
	}
	ack, err := readUDPMessage(conn)
	if err != nil {
		t.Fatalf("read acknowledgement failed: expected nil error, received %v", err)
	}
	if ack.Type != ackType {
		t.Fatalf("acknowledgement type mismatch: expected %q, received %q", ackType, ack.Type)
	}
	if ack.Sequence != sequence {
		t.Fatalf("acknowledgement sequence mismatch: expected %d, received %d", sequence, ack.Sequence)
	}
}
