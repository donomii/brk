package brk

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
)

var fuzzTransactionID = [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

func FuzzDecodeUDPMessage(f *testing.F) {
	seeds := make(map[string]struct{})
	messages := []UdpMessage{
		{Data: []byte("legacy"), Port: 9000, Sequence: 7},
		{Data: []byte("json"), Port: 9000, Sequence: 42, Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")},
		{Port: 9000, Sequence: 43, Type: ackType, Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")},
		{Data: []byte("fragment"), Port: 9000, Sequence: 44, Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100"), FragmentGroup: "11223344556677889900aabbccddeeff", FragmentIndex: 1, FragmentCount: 3},
		{Data: []byte("binary"), Port: 9000, Sequence: 45, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")},
		{Port: 9000, Sequence: 46, Type: ackType, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")},
		{Data: []byte("fragment"), Port: 9000, Sequence: 47, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100"), FragmentGroup: "11223344556677889900aabbccddeeff", FragmentIndex: 1, FragmentCount: 3},
	}
	for _, message := range messages {
		packet, err := encodeUDPMessage(message)
		if err != nil {
			f.Fatalf("encode wire fuzz seed failed: expected nil error for version %d sequence %d, received %v", message.Version, message.Sequence, err)
		}
		addPacketAndTruncations(f, seeds, packet)
	}
	signed, err := applyMessageAuthentication(messages[4], bytes.Repeat([]byte("k"), minimumSharedKeyBytes))
	if err != nil {
		f.Fatalf("authenticate wire fuzz seed failed: expected nil error, received %v", err)
	}
	signedPacket, err := encodeUDPMessage(signed)
	if err != nil {
		f.Fatalf("encode signed wire fuzz seed failed: expected nil error, received %v", err)
	}
	addFuzzPacket(f, seeds, signedPacket)
	addFuzzPacket(f, seeds, []byte(`{"v":1,"kind":"data","sid":"00112233445566778899aabbccddeeff","mid":"ffeeddccbbaa99887766554433221100","seq":42,"unknown":true}`))
	addFuzzPacket(f, seeds, []byte{0x00, 0x01, 0x02, 0xff})
	addFuzzPacket(f, seeds, bytes.Repeat([]byte("x"), packetReadSize))

	source := &net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 12345}
	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > packetReadSize {
			packet = packet[:packetReadSize]
		}
		message, err := decodeUDPMessage(packet, source)
		if err != nil {
			return
		}
		if len(packet) > maxPacketSize {
			t.Fatalf("wire parser accepted oversized packet: expected at most %d bytes, received %d", maxPacketSize, len(packet))
		}
		if message.Address != source.IP.String() || message.Port != source.Port {
			t.Fatalf("wire parser source mismatch: expected %s:%d, received %s:%d", source.IP, source.Port, message.Address, message.Port)
		}
		encoded, err := encodeUDPMessage(message)
		if err != nil {
			t.Fatalf("wire parser produced unencodable message: version=%d sequence=%d error=%v", message.Version, message.Sequence, err)
		}
		roundTrip, err := decodeUDPMessage(encoded, source)
		if err != nil {
			t.Fatalf("decode canonical wire packet failed: version=%d sequence=%d encoded_bytes=%d error=%v", message.Version, message.Sequence, len(encoded), err)
		}
		requireSameWireMessage(t, message, roundTrip)
	})
}

func FuzzParseSTUNBindingResponse(f *testing.F) {
	seeds := make(map[string]struct{})
	validIPv4 := makeSTUNBindingSuccess(fuzzTransactionID, netip.MustParseAddrPort("203.0.113.9:54321"))
	validIPv6 := makeSTUNBindingSuccess(fuzzTransactionID, netip.MustParseAddrPort("[2001:db8::9]:4567"))
	validMapped := makeSTUNMappedIPv4Response(fuzzTransactionID, "198.51.100.4", 12345)
	addPacketAndTruncations(f, seeds, validIPv4)
	addPacketAndTruncations(f, seeds, validIPv6)
	addFuzzPacket(f, seeds, validMapped)

	longMessage := append([]byte(nil), validIPv4...)
	binary.BigEndian.PutUint16(longMessage[2:4], ^uint16(0))
	shortMessage := append([]byte(nil), validIPv4...)
	binary.BigEndian.PutUint16(shortMessage[2:4], 4)
	longAttribute := append([]byte(nil), validIPv4...)
	binary.BigEndian.PutUint16(longAttribute[22:24], ^uint16(0))
	unknownAttribute := append([]byte(nil), validIPv4...)
	binary.BigEndian.PutUint16(unknownAttribute[20:22], 0x7fff)
	addFuzzPacket(f, seeds, longMessage)
	addFuzzPacket(f, seeds, shortMessage)
	addFuzzPacket(f, seeds, longAttribute)
	addFuzzPacket(f, seeds, unknownAttribute)
	addFuzzPacket(f, seeds, append(append([]byte(nil), validIPv4...), 0))
	addFuzzPacket(f, seeds, []byte{0x01, 0x01, 0x00, 0x00, 0x21, 0x12, 0xa4, 0x42})

	maximumPacketSize := len(makeSTUNBindingRequest([12]byte{})) + int(^uint16(0))
	f.Fuzz(func(t *testing.T, packet []byte) {
		if len(packet) > maximumPacketSize+1 {
			packet = packet[:maximumPacketSize+1]
		}
		address, err := parseSTUNBindingResponse(packet, fuzzTransactionID, "fuzz.invalid:3478")
		if err != nil {
			return
		}
		if err := validateSTUNPacket(packet); err != nil {
			t.Fatalf("STUN response parser accepted invalid packet: bytes=%d error=%v", len(packet), err)
		}
		parsedIP, err := netip.ParseAddr(address.IP)
		if err != nil {
			t.Fatalf("STUN response parser produced invalid IP address %q: %v", address.IP, err)
		}
		if address.Port < 0 || address.Port > int(^uint16(0)) {
			t.Fatalf("STUN response parser produced invalid port: expected 0..%d, received %d", ^uint16(0), address.Port)
		}
		expectedFamily := "IPv6"
		if parsedIP.Is4() {
			expectedFamily = "IPv4"
		}
		if address.Family != expectedFamily || address.Server != "fuzz.invalid:3478" {
			t.Fatalf("STUN response metadata mismatch: expected family=%s server=%q, received family=%s server=%q", expectedFamily, "fuzz.invalid:3478", address.Family, address.Server)
		}
		canonical := makeSTUNBindingSuccess(fuzzTransactionID, netip.AddrPortFrom(parsedIP, uint16(address.Port)))
		roundTrip, err := parseSTUNBindingResponse(canonical, fuzzTransactionID, address.Server)
		if err != nil {
			t.Fatalf("decode canonical STUN response failed: address=%s:%d error=%v", address.IP, address.Port, err)
		}
		if roundTrip != address {
			t.Fatalf("STUN response round trip mismatch: expected %+v, received %+v", address, roundTrip)
		}
	})
}

func addPacketAndTruncations(f *testing.F, seeds map[string]struct{}, packet []byte) {
	addFuzzPacket(f, seeds, packet)
	for _, length := range []int{0, 1, len(packet) / 2, len(packet) - 1} {
		addFuzzPacket(f, seeds, packet[:length])
	}
}

func addFuzzPacket(f *testing.F, seeds map[string]struct{}, packet []byte) {
	key := string(packet)
	if _, exists := seeds[key]; exists {
		return
	}
	seeds[key] = struct{}{}
	f.Add(append([]byte(nil), packet...))
}

func requireSameWireMessage(t *testing.T, expected, received UdpMessage) {
	t.Helper()
	if expected.Version != received.Version || expected.Sequence != received.Sequence || expected.Type != received.Type || expected.SessionID != received.SessionID || expected.MessageID != received.MessageID {
		t.Fatalf("wire round trip identity mismatch: expected version=%d sequence=%d type=%q session=%q message=%q, received version=%d sequence=%d type=%q session=%q message=%q", expected.Version, expected.Sequence, expected.Type, expected.SessionID, expected.MessageID, received.Version, received.Sequence, received.Type, received.SessionID, received.MessageID)
	}
	if expected.Address != received.Address || expected.Port != received.Port || !expected.Cached.Equal(received.Cached) {
		t.Fatalf("wire round trip transport mismatch: expected address=%q port=%d cached=%v, received address=%q port=%d cached=%v", expected.Address, expected.Port, expected.Cached, received.Address, received.Port, received.Cached)
	}
	if expected.Signature != received.Signature || expected.FragmentGroup != received.FragmentGroup || expected.FragmentIndex != received.FragmentIndex || expected.FragmentCount != received.FragmentCount {
		t.Fatalf("wire round trip metadata mismatch: expected signature_bytes=%d group=%q index=%d count=%d, received signature_bytes=%d group=%q index=%d count=%d", len(expected.Signature), expected.FragmentGroup, expected.FragmentIndex, expected.FragmentCount, len(received.Signature), received.FragmentGroup, received.FragmentIndex, received.FragmentCount)
	}
	if !bytes.Equal(expected.Data, received.Data) {
		t.Fatalf("wire round trip payload mismatch: expected %d bytes, received %d bytes", len(expected.Data), len(received.Data))
	}
}
