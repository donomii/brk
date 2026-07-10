package brk

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestProtocolV1WireRoundTripAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Data: []byte("hello"), Address: "127.0.0.1", Port: 9000, Sequence: 42, Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100"), Deadline: time.Now().Add(time.Minute)}
	authenticated, err := applyMessageAuthentication(message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v1 message failed: expected nil error, received %v", err)
	}
	packet, err := encodeUDPMessage(authenticated)
	if err != nil {
		t.Fatalf("encode protocol v1 message failed: expected nil error, received %v", err)
	}
	if bytes.Contains(packet, []byte("Address")) || bytes.Contains(packet, []byte("Deadline")) {
		t.Fatalf("protocol v1 wire fields mismatch: expected transport-only fields to be absent, received %s", packet)
	}

	decoded, err := decodeUDPMessage(packet, &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 8000})
	if err != nil {
		t.Fatalf("decode protocol v1 message failed: expected nil error, received %v", err)
	}
	if err := verifyMessageAuthentication(decoded, key); err != nil {
		t.Fatalf("verify protocol v1 message failed: expected nil error, received %v", err)
	}
	if decoded.SessionID != message.SessionID || decoded.MessageID != message.MessageID || decoded.Sequence != message.Sequence || string(decoded.Data) != "hello" {
		t.Fatalf("protocol v1 round trip mismatch: expected session=%s message=%s sequence=%d data=%q, received session=%s message=%s sequence=%d data=%q", message.SessionID, message.MessageID, message.Sequence, message.Data, decoded.SessionID, decoded.MessageID, decoded.Sequence, decoded.Data)
	}
	if decoded.Address != "127.0.0.2" || decoded.Port != 8000 {
		t.Fatalf("protocol v1 source mismatch: expected 127.0.0.2:8000, received %s:%d", decoded.Address, decoded.Port)
	}
}

func TestProtocolV1AuthenticationRejectsAlteredMessage(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Data: []byte("hello"), Port: 9000, Sequence: 42, Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	authenticated, err := applyMessageAuthentication(message, key)
	if err != nil {
		t.Fatalf("authenticate protocol v1 message failed: expected nil error, received %v", err)
	}
	authenticated.Data = []byte("altered")
	if err := verifyMessageAuthentication(authenticated, key); err == nil {
		t.Fatalf("altered protocol v1 authentication mismatch: expected error, received nil")
	}
}

func TestProtocolV1StrictDecodeRejectsUnknownField(t *testing.T) {
	packet := []byte(`{"v":1,"kind":"data","sid":"00112233445566778899aabbccddeeff","mid":"ffeeddccbbaa99887766554433221100","seq":42,"extra":true}`)
	_, err := decodeWireMessage(packet)
	if err == nil {
		t.Fatalf("unknown protocol v1 field mismatch: expected error, received nil")
	}
}

func TestProtocolV1StrictDecodeRejectsInvalidPackets(t *testing.T) {
	packets := map[string]string{
		"unsupported version":  `{"v":2,"kind":"data","sid":"00112233445566778899aabbccddeeff","mid":"ffeeddccbbaa99887766554433221100","seq":42}`,
		"trailing value":       `{"v":1,"kind":"data","sid":"00112233445566778899aabbccddeeff","mid":"ffeeddccbbaa99887766554433221100","seq":42} {}`,
		"invalid session":      `{"v":1,"kind":"data","sid":"invalid","mid":"ffeeddccbbaa99887766554433221100","seq":42}`,
		"invalid message":      `{"v":1,"kind":"data","sid":"00112233445566778899aabbccddeeff","mid":"invalid","seq":42}`,
		"unknown kind":         `{"v":1,"kind":"other","sid":"00112233445566778899aabbccddeeff","mid":"ffeeddccbbaa99887766554433221100","seq":42}`,
		"acknowledgement data": `{"v":1,"kind":"ack","sid":"00112233445566778899aabbccddeeff","mid":"ffeeddccbbaa99887766554433221100","seq":42,"data":"YQ=="}`,
	}
	for name, packet := range packets {
		t.Run(name, func(t *testing.T) {
			_, err := decodeWireMessage([]byte(packet))
			if err == nil {
				t.Fatalf("invalid protocol v1 packet mismatch: expected error, received nil for %s", packet)
			}
		})
	}
}

func TestProtocolV1AuthenticationPolicyRejectsUnsignedAndUnexpectedSignedPackets(t *testing.T) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	if err := verifyMessageAuthentication(message, key); err == nil {
		t.Fatalf("unsigned packet with configured key mismatch: expected error, received nil")
	}
	message.Signature = "unexpected"
	if err := verifyMessageAuthentication(message, nil); err == nil {
		t.Fatalf("signed packet without configured key mismatch: expected error, received nil")
	}
	legacy := UdpMessage{Version: ProtocolLegacy}
	if err := verifyMessageAuthentication(legacy, key); err == nil {
		t.Fatalf("legacy packet with configured key mismatch: expected error, received nil")
	}
}

func TestLegacyWireFormatRemainsCompatible(t *testing.T) {
	message := UdpMessage{Data: []byte("legacy"), Address: "127.0.0.1", Port: 9000, Sequence: 7, Cached: time.Now()}
	packet, err := encodeUDPMessage(message)
	if err != nil {
		t.Fatalf("encode legacy message failed: expected nil error, received %v", err)
	}
	if bytes.Contains(packet, []byte("Version")) || bytes.Contains(packet, []byte(`"v"`)) {
		t.Fatalf("legacy wire format mismatch: expected no version field, received %s", packet)
	}
	decoded, err := decodeWireMessage(packet)
	if err != nil {
		t.Fatalf("decode legacy message failed: expected nil error, received %v", err)
	}
	if decoded.Version != ProtocolLegacy || decoded.Sequence != message.Sequence || string(decoded.Data) != "legacy" {
		t.Fatalf("legacy round trip mismatch: expected version=%d sequence=%d data=%q, received version=%d sequence=%d data=%q", ProtocolLegacy, message.Sequence, message.Data, decoded.Version, decoded.Sequence, decoded.Data)
	}
}

func TestResolveRetryConfigCopiesAuthenticationKey(t *testing.T) {
	key := bytes.Repeat([]byte("s"), minimumSharedKeyBytes)
	config, err := ResolveRetryConfig(RetryConfig{AuthenticationKey: key})
	if err != nil {
		t.Fatalf("resolve authenticated retry config failed: expected nil error, received %v", err)
	}
	key[0] = 'x'
	if config.AuthenticationKey[0] != 's' {
		t.Fatalf("authentication key ownership mismatch: expected copied first byte %q, received %q", 's', config.AuthenticationKey[0])
	}
}

func TestResolveRetryConfigDisableOptionsOverridePositiveValues(t *testing.T) {
	config, err := ResolveRetryConfig(RetryConfig{JitterFraction: 0.5, DisableJitter: true, DeliveryTimeout: time.Second, DisableDeliveryTimeout: true})
	if err != nil {
		t.Fatalf("resolve disabled retry options failed: expected nil error, received %v", err)
	}
	if config.JitterFraction != 0 || config.DeliveryTimeout != 0 {
		t.Fatalf("disabled retry options mismatch: expected zero jitter and timeout, received jitter=%v timeout=%v", config.JitterFraction, config.DeliveryTimeout)
	}
}

func TestRetryDelayUsesExponentialBackoffAndCap(t *testing.T) {
	config, err := ResolveRetryConfig(RetryConfig{AckTimeout: time.Second, BackoffMultiplier: 2, MaxRetryDelay: 3 * time.Second, DisableJitter: true})
	if err != nil {
		t.Fatalf("resolve backoff retry config failed: expected nil error, received %v", err)
	}
	messageID := MessageID("ffeeddccbbaa99887766554433221100")
	expected := []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}
	for index, delay := range expected {
		received := retryDelay(config, messageID, index+1)
		if received != delay {
			t.Fatalf("retry delay %d mismatch: expected %v, received %v", index+1, delay, received)
		}
	}
}

func BenchmarkProtocolV1Encode(b *testing.B) {
	key := bytes.Repeat([]byte("k"), minimumSharedKeyBytes)
	message := UdpMessage{Data: bytes.Repeat([]byte("payload"), 128), Address: "127.0.0.1", Port: 9000, Sequence: 42, Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	authenticated, err := applyMessageAuthentication(message, key)
	if err != nil {
		b.Fatalf("authenticate benchmark message failed: expected nil error, received %v", err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(message.Data)))
	for iteration := 0; iteration < b.N; iteration++ {
		_, err = encodeUDPMessage(authenticated)
		if err != nil {
			b.Fatalf("encode benchmark message failed: expected nil error, received %v", err)
		}
	}
}
