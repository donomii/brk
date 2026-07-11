package brk

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"
)

func TestProtocolV2WireRoundTripAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Data: []byte("hello"), Address: "127.0.0.1", Port: 9000, Sequence: 42, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100"), Deadline: time.Now().Add(time.Minute)}
	authenticated, err := applyMessageAuthentication(message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v2 message failed: expected nil error, received %v", err)
	}
	packet, err := encodeUDPMessage(authenticated)
	if err != nil {
		t.Fatalf("encode protocol v2 message failed: expected nil error, received %v", err)
	}
	if packet[0] != binaryWireMagic {
		t.Fatalf("protocol v2 magic mismatch: expected %#02x, received %#02x", binaryWireMagic, packet[0])
	}
	if len(packet) != binaryHeaderSize+len(message.Data)+binarySignatureSize {
		t.Fatalf("protocol v2 packet size mismatch: expected %d bytes, received %d", binaryHeaderSize+len(message.Data)+binarySignatureSize, len(packet))
	}

	v1Message := message
	v1Message.Version = ProtocolV1
	v1Signed, err := applyMessageAuthentication(v1Message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v1 message failed: expected nil error, received %v", err)
	}
	v1Packet, err := encodeUDPMessage(v1Signed)
	if err != nil {
		t.Fatalf("encode protocol v1 message failed: expected nil error, received %v", err)
	}
	if len(packet) >= len(v1Packet) {
		t.Fatalf("protocol v2 compactness mismatch: expected fewer bytes than v1's %d, received %d", len(v1Packet), len(packet))
	}

	decoded, err := decodeUDPMessage(packet, &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 8000})
	if err != nil {
		t.Fatalf("decode protocol v2 message failed: expected nil error, received %v", err)
	}
	if err := verifyMessageAuthentication(decoded, key); err != nil {
		t.Fatalf("verify protocol v2 message failed: expected nil error, received %v", err)
	}
	if decoded.Version != ProtocolV2 || decoded.SessionID != message.SessionID || decoded.MessageID != message.MessageID || decoded.Sequence != message.Sequence || string(decoded.Data) != "hello" {
		t.Fatalf("protocol v2 round trip mismatch: expected version=2 session=%s message=%s sequence=%d data=%q, received version=%d session=%s message=%s sequence=%d data=%q", message.SessionID, message.MessageID, message.Sequence, message.Data, decoded.Version, decoded.SessionID, decoded.MessageID, decoded.Sequence, decoded.Data)
	}
	if decoded.Address != "127.0.0.2" || decoded.Port != 8000 {
		t.Fatalf("protocol v2 source mismatch: expected 127.0.0.2:8000, received %s:%d", decoded.Address, decoded.Port)
	}
}

func TestProtocolV2AckRoundTrip(t *testing.T) {
	ack := UdpMessage{Port: 9000, Sequence: 7, Type: ackType, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	packet, err := encodeUDPMessage(ack)
	if err != nil {
		t.Fatalf("encode protocol v2 acknowledgement failed: expected nil error, received %v", err)
	}
	if len(packet) != binaryHeaderSize {
		t.Fatalf("protocol v2 acknowledgement size mismatch: expected %d bytes, received %d", binaryHeaderSize, len(packet))
	}

	decoded, err := decodeWireMessage(packet)
	if err != nil {
		t.Fatalf("decode protocol v2 acknowledgement failed: expected nil error, received %v", err)
	}
	if decoded.Type != ackType || decoded.Sequence != 7 || decoded.SessionID != ack.SessionID || decoded.MessageID != ack.MessageID || len(decoded.Data) != 0 {
		t.Fatalf("protocol v2 acknowledgement mismatch: expected ack sequence 7 with empty data, received %+v", decoded)
	}
}

func TestProtocolV2AuthenticationRejectsAlteredPacket(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Data: []byte("hello"), Port: 9000, Sequence: 42, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	authenticated, err := applyMessageAuthentication(message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v2 message failed: expected nil error, received %v", err)
	}
	packet, err := encodeUDPMessage(authenticated)
	if err != nil {
		t.Fatalf("encode protocol v2 message failed: expected nil error, received %v", err)
	}

	packet[binaryHeaderSize] = packet[binaryHeaderSize] ^ 0xFF
	decoded, err := decodeWireMessage(packet)
	if err != nil {
		t.Fatalf("decode altered protocol v2 message failed: expected nil decode error, received %v", err)
	}
	if err := verifyMessageAuthentication(decoded, key); err == nil {
		t.Fatalf("altered protocol v2 authentication mismatch: expected error, received nil")
	}
}

func TestProtocolV2SignatureDoesNotValidateAcrossVersions(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Data: []byte("hello"), Port: 9000, Sequence: 42, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	signedV2, err := applyMessageAuthentication(message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v2 message failed: expected nil error, received %v", err)
	}

	v1Message := message
	v1Message.Version = ProtocolV1
	signedV1, err := applyMessageAuthentication(v1Message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v1 message failed: expected nil error, received %v", err)
	}
	if signedV1.Signature == signedV2.Signature {
		t.Fatalf("cross-version signature mismatch: expected distinct signatures for v1 and v2 payloads")
	}

	relabeled := signedV2
	relabeled.Version = ProtocolV1
	if err := verifyMessageAuthentication(relabeled, key); err == nil {
		t.Fatalf("relabeled signature mismatch: expected v2 signature to fail v1 verification, received nil error")
	}
}

func TestProtocolV2DecodeRejectsMalformedPackets(t *testing.T) {
	valid, err := encodeUDPMessage(UdpMessage{Data: []byte("hello"), Port: 9000, Sequence: 42, Version: ProtocolV2, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")})
	if err != nil {
		t.Fatalf("encode protocol v2 message failed: expected nil error, received %v", err)
	}

	wrongVersion := append([]byte(nil), valid...)
	wrongVersion[1] = 3
	unknownFlag := append([]byte(nil), valid...)
	unknownFlag[3] = 0x02
	unknownKind := append([]byte(nil), valid...)
	unknownKind[2] = 9
	ackWithPayload := append([]byte(nil), valid...)
	ackWithPayload[2] = binaryKindAck
	signedTooShort := append([]byte(nil), valid[:binaryHeaderSize]...)
	signedTooShort[3] = binaryFlagSigned
	oversizedSequence := append([]byte(nil), valid...)
	for index := 36; index < 44; index++ {
		oversizedSequence[index] = 0xFF
	}

	tests := []struct {
		name   string
		packet []byte
	}{
		{name: "short header", packet: valid[:binaryHeaderSize-1]},
		{name: "wrong version byte", packet: wrongVersion},
		{name: "unknown flag", packet: unknownFlag},
		{name: "unknown kind", packet: unknownKind},
		{name: "acknowledgement with payload", packet: ackWithPayload},
		{name: "signed without trailer", packet: signedTooShort},
		{name: "oversized sequence", packet: oversizedSequence},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeWireMessage(test.packet)
			if err == nil {
				t.Fatalf("malformed protocol v2 packet mismatch: expected error, received nil")
			}
		})
	}
}

func TestResolveRetryConfigWireVersion(t *testing.T) {
	resolved, err := ResolveRetryConfig(RetryConfig{})
	if err != nil {
		t.Fatalf("resolve default wire version failed: expected nil error, received %v", err)
	}
	if resolved.WireVersion != ProtocolV1 {
		t.Fatalf("default wire version mismatch: expected %d, received %d", ProtocolV1, resolved.WireVersion)
	}

	resolved, err = ResolveRetryConfig(RetryConfig{WireVersion: ProtocolV2})
	if err != nil {
		t.Fatalf("resolve v2 wire version failed: expected nil error, received %v", err)
	}
	if resolved.WireVersion != ProtocolV2 {
		t.Fatalf("v2 wire version mismatch: expected %d, received %d", ProtocolV2, resolved.WireVersion)
	}

	_, err = ResolveRetryConfig(RetryConfig{WireVersion: 3})
	if err == nil {
		t.Fatalf("invalid wire version mismatch: expected error, received nil")
	}
}

func TestRetryServersExchangeProtocolV2(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	delivered := make(chan UdpMessage, 1)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, AuthenticationKey: key}, func(incoming, outgoing chan UdpMessage) {
		for message := range incoming {
			delivered <- message
		}
	})
	if err != nil {
		t.Fatalf("start v1 receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)

	sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, AuthenticationKey: key, WireVersion: ProtocolV2}, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start v2 sender failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, sender)

	host, port := splitHostPort(t, receiver.LocalAddress())
	sender.Outgoing <- UdpMessage{Data: []byte("binary hello"), Address: host, Port: port}

	message := receiveMessage(t, delivered)
	if string(message.Data) != "binary hello" {
		t.Fatalf("v2 delivery mismatch: expected %q, received %q", "binary hello", string(message.Data))
	}
	if message.Version != ProtocolV2 {
		t.Fatalf("v2 delivery version mismatch: expected %d, received %d", ProtocolV2, message.Version)
	}
	waitForStats(t, sender, func(snapshot DeliveryStatsSnapshot) bool {
		return snapshot.Acked >= 1
	})
}
