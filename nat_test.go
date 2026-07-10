package brk

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestNATConfigDefaultsAndValidation(t *testing.T) {
	punch, err := ResolvePunchConfig(PunchConfig{})
	if err != nil {
		t.Fatalf("resolve punch defaults failed: expected nil error, received %v", err)
	}
	if punch.Attempts != 8 || punch.Interval != 250*time.Millisecond {
		t.Fatalf("punch defaults mismatch: expected attempts=8 interval=%v, received %+v", 250*time.Millisecond, punch)
	}
	keepalive, err := ResolveKeepaliveConfig(KeepaliveConfig{})
	if err != nil {
		t.Fatalf("resolve keepalive defaults failed: expected nil error, received %v", err)
	}
	if keepalive.Interval != 20*time.Second {
		t.Fatalf("keepalive default mismatch: expected %v, received %v", 20*time.Second, keepalive.Interval)
	}
	if _, err = ResolvePunchConfig(PunchConfig{Attempts: -1}); err == nil {
		t.Fatalf("negative punch attempts mismatch: expected error, received nil")
	}
	if _, err = ResolveKeepaliveConfig(KeepaliveConfig{Interval: -1}); err == nil {
		t.Fatalf("negative keepalive interval mismatch: expected error, received nil")
	}
}

func TestIPv6UdpServerRoundTrip(t *testing.T) {
	received := make(chan UdpMessage, 1)
	receiver, err := StartUdpContext(context.Background(), "::1", "0", func(incoming, outgoing chan UdpMessage) {
		received <- <-incoming
	})
	if err != nil {
		t.Fatalf("start IPv6 UDP receiver failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, receiver)
	sender, err := StartUdpContext(context.Background(), "::1", "0", func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start IPv6 UDP sender failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, sender)

	err = SendMessageTo(sender.Outgoing, []byte("ipv6"), receiver.LocalEndpoint())
	if err != nil {
		t.Fatalf("send IPv6 UDP message failed: expected nil error, received %v", err)
	}
	message := receiveMessage(t, received)
	if string(message.Data) != "ipv6" {
		t.Fatalf("IPv6 delivery mismatch: expected %q, received %q", "ipv6", message.Data)
	}
	endpoint, err := message.Endpoint()
	if err != nil {
		t.Fatalf("parse IPv6 source endpoint failed: expected nil error, received %v", err)
	}
	if endpoint != sender.LocalEndpoint() {
		t.Fatalf("IPv6 source endpoint mismatch: expected %v, received %v", sender.LocalEndpoint(), endpoint)
	}
}

func TestLiveSTUNUsesUdpServerSocket(t *testing.T) {
	stunServer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("start live STUN fixture failed: expected nil error, received %v", err)
	}
	defer closeUDPConn(t, stunServer)
	err = stunServer.SetDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("set live STUN fixture deadline failed: expected nil error, received %v", err)
	}
	sourceResult := make(chan netip.AddrPort, 1)
	fixtureResult := make(chan error, 1)
	go serveLiveSTUNResponse(stunServer, sourceResult, fixtureResult)

	server, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
	})
	if err != nil {
		t.Fatalf("start live STUN UDP server failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, server)
	address, err := server.DiscoverExternalAddress(context.Background(), STUNConfig{Server: stunServer.LocalAddr().String(), Timeout: time.Second})
	if err != nil {
		t.Fatalf("discover live external address failed: expected nil error, received %v", err)
	}
	if fixtureErr := <-fixtureResult; fixtureErr != nil {
		t.Fatalf("serve live STUN response failed: expected nil error, received %v", fixtureErr)
	}
	requestSource := <-sourceResult
	if requestSource != server.LocalEndpoint() {
		t.Fatalf("live STUN source mismatch: expected server endpoint %v, received %v", server.LocalEndpoint(), requestSource)
	}
	external, err := address.Endpoint()
	if err != nil {
		t.Fatalf("parse live external endpoint failed: expected nil error, received %v", err)
	}
	if external != server.LocalEndpoint() {
		t.Fatalf("live external endpoint mismatch: expected %v, received %v", server.LocalEndpoint(), external)
	}
}

func serveLiveSTUNResponse(server *net.UDPConn, sourceResult chan<- netip.AddrPort, result chan<- error) {
	packet := make([]byte, 1500)
	n, source, err := server.ReadFromUDPAddrPort(packet)
	if err != nil {
		result <- fmt.Errorf("read live STUN request: %w", err)
		return
	}
	packet = packet[:n]
	if !isSTUNPacket(packet) || binary.BigEndian.Uint16(packet[0:2]) != stunBindingRequest {
		result <- fmt.Errorf("live STUN request mismatch: expected binding request, received %x", packet)
		return
	}
	source = normalizeEndpoint(source)
	sourceResult <- source
	_, err = server.WriteToUDPAddrPort(makeSTUNBindingSuccess(stunTransactionID(packet), source), source)
	if err != nil {
		result <- fmt.Errorf("write live STUN response: %w", err)
		return
	}
	result <- nil
}

func TestPunchPeerReturnsObservedEndpoint(t *testing.T) {
	peer, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
	})
	if err != nil {
		t.Fatalf("start punch peer failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, peer)
	server, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
	})
	if err != nil {
		t.Fatalf("start punch sender failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, server)

	result, err := server.PunchPeer(context.Background(), peer.LocalEndpoint(), PunchConfig{Attempts: 2, Interval: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("punch UDP peer failed: expected nil error, received %v", err)
	}
	if result.Peer != peer.LocalEndpoint() || result.ObservedAddress != server.LocalEndpoint() || result.Attempts != 1 {
		t.Fatalf("punch result mismatch: expected peer=%v observed=%v attempts=1, received %+v", peer.LocalEndpoint(), server.LocalEndpoint(), result)
	}
}

func TestKeepPeerAliveSendsRepeatedIndications(t *testing.T) {
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("start keepalive peer failed: expected nil error, received %v", err)
	}
	defer closeUDPConn(t, peer)
	err = peer.SetDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("set keepalive peer deadline failed: expected nil error, received %v", err)
	}
	server, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start keepalive server failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	keepaliveResult := make(chan error, 1)
	go func() {
		keepaliveResult <- server.KeepPeerAlive(ctx, normalizeEndpoint(peer.LocalAddr().(*net.UDPAddr).AddrPort()), KeepaliveConfig{Interval: 20 * time.Millisecond})
	}()

	for attempt := 0; attempt < 2; attempt++ {
		packet := make([]byte, 1500)
		n, _, readErr := peer.ReadFromUDPAddrPort(packet)
		if readErr != nil {
			t.Fatalf("read keepalive indication %d failed: expected nil error, received %v", attempt+1, readErr)
		}
		packet = packet[:n]
		if binary.BigEndian.Uint16(packet[0:2]) != stunBindingIndication || !hasSTUNSoftware(packet, brkSTUNSoftware) {
			t.Fatalf("keepalive indication %d mismatch: expected marked binding indication, received %x", attempt+1, packet)
		}
	}
	cancel()
	select {
	case err = <-keepaliveResult:
		if err != nil {
			t.Fatalf("stop keepalive loop failed: expected nil error, received %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("stop keepalive loop timed out: expected completion within %v", time.Second)
	}
}

func TestIPv6XORMappedAddressRoundTrip(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	endpoint := netip.MustParseAddrPort("[2001:db8::9]:54321")
	packet := makeSTUNBindingSuccess(transactionID, endpoint)
	address, err := parseSTUNBindingResponse(packet, transactionID, "[::1]:3478")
	if err != nil {
		t.Fatalf("parse IPv6 XOR mapped address failed: expected nil error, received %v", err)
	}
	received, err := address.Endpoint()
	if err != nil {
		t.Fatalf("parse IPv6 external endpoint failed: expected nil error, received %v", err)
	}
	if received != endpoint {
		t.Fatalf("IPv6 XOR mapped endpoint mismatch: expected %v, received %v", endpoint, received)
	}
}
