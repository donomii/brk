package brk

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"time"
)

type legacyWireMessage struct {
	Data     []byte
	Address  string
	Port     int
	Sequence int
	Type     string
	Cached   time.Time
}

type v1WireMessage struct {
	Version   ProtocolVersion `json:"v"`
	Kind      string          `json:"kind"`
	SessionID SessionID       `json:"sid"`
	MessageID MessageID       `json:"mid"`
	Sequence  int             `json:"seq"`
	Data      []byte          `json:"data,omitempty"`
	Auth      string          `json:"auth,omitempty"`
}

// wireEnvelope is the canonical unsigned message form. It is the version 1
// JSON body and, for every versioned format, the HMAC authentication payload,
// so a signature never validates across protocol versions.
type wireEnvelope struct {
	Version   ProtocolVersion `json:"v"`
	Kind      string          `json:"kind"`
	SessionID SessionID       `json:"sid"`
	MessageID MessageID       `json:"mid"`
	Sequence  int             `json:"seq"`
	Data      []byte          `json:"data,omitempty"`
}

func newSessionID() (SessionID, error) {
	value, err := newWireID("session")
	return SessionID(value), err
}

func newMessageID() (MessageID, error) {
	value, err := newWireID("message")
	return MessageID(value), err
}

func newWireID(kind string) (string, error) {
	value := make([]byte, 16)
	_, err := rand.Read(value)
	if err != nil {
		return "", fmt.Errorf("create %s id failed: expected 16 random bytes: %w", kind, err)
	}
	return hex.EncodeToString(value), nil
}

func applyMessageAuthentication(message UdpMessage, key []byte) (UdpMessage, error) {
	if len(key) == 0 {
		message.Signature = ""
		return message, nil
	}
	payload, err := messageAuthenticationPayload(message)
	if err != nil {
		return UdpMessage{}, err
	}
	mac := hmac.New(sha256.New, key)
	_, err = mac.Write(payload)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("authenticate UDP message %q failed: %w", message.MessageID, err)
	}
	message.Signature = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return message, nil
}

func verifyMessageAuthentication(message UdpMessage, key []byte) error {
	if len(key) == 0 {
		if message.Signature != "" {
			return fmt.Errorf("authenticate UDP message %q failed: expected unsigned packet because no key is configured", message.MessageID)
		}
		return nil
	}
	if message.Signature == "" {
		return fmt.Errorf("authenticate UDP message %q failed: expected HMAC signature, received empty signature", message.MessageID)
	}
	received, err := base64.RawURLEncoding.DecodeString(message.Signature)
	if err != nil {
		return fmt.Errorf("authenticate UDP message %q failed: decode signature: %w", message.MessageID, err)
	}
	payload, err := messageAuthenticationPayload(message)
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, key)
	_, err = mac.Write(payload)
	if err != nil {
		return fmt.Errorf("authenticate UDP message %q failed: %w", message.MessageID, err)
	}
	if !hmac.Equal(received, mac.Sum(nil)) {
		return fmt.Errorf("authenticate UDP message %q failed: signature did not match packet contents", message.MessageID)
	}
	return nil
}

func messageAuthenticationPayload(message UdpMessage) ([]byte, error) {
	wire, err := makeWireEnvelope(message)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("encode UDP message %q authentication payload: %w", message.MessageID, err)
	}
	return payload, nil
}

func encodeUDPMessage(message UdpMessage) ([]byte, error) {
	if message.Port < 1 || message.Port > 65535 {
		return nil, fmt.Errorf("expected UDP target port in 1..65535, received %d", message.Port)
	}
	var packet []byte
	var err error
	if message.Version == ProtocolLegacy {
		packet, err = json.Marshal(legacyMessageForWire(message))
	} else if message.Version == ProtocolV1 {
		wire, wireErr := v1MessageForWire(message)
		if wireErr != nil {
			return nil, wireErr
		}
		packet, err = json.Marshal(wire)
	} else if message.Version == ProtocolV2 {
		packet, err = encodeBinaryWireMessage(message)
	} else {
		return nil, fmt.Errorf("encode UDP message failed: expected protocol version 0, 1, or 2, received %d", message.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("encode UDP message sequence %d as JSON: %w", message.Sequence, err)
	}
	if len(packet) > maxPacketSize {
		return nil, fmt.Errorf("encode UDP message sequence %d for UDP: expected at most %d encoded bytes, received %d from %d payload bytes", message.Sequence, maxPacketSize, len(packet), len(message.Data))
	}
	return packet, nil
}

func decodeUDPMessage(packet []byte, addr *net.UDPAddr) (UdpMessage, error) {
	if len(packet) > maxPacketSize {
		return UdpMessage{}, fmt.Errorf("decode UDP packet from %v: expected at most %d encoded bytes, received at least %d", addr, maxPacketSize, len(packet))
	}
	message, err := decodeWireMessage(packet)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("decode UDP packet from %v: %w", addr, err)
	}
	message.Address = addr.IP.String()
	message.Port = addr.Port
	return message, nil
}

func decodeWireMessage(packet []byte) (UdpMessage, error) {
	if isBinaryWirePacket(packet) {
		return decodeBinaryWireMessage(packet)
	}
	var probe struct {
		Version *ProtocolVersion `json:"v"`
	}
	err := json.Unmarshal(packet, &probe)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("decode JSON envelope: %w", err)
	}
	if probe.Version == nil {
		return decodeLegacyWireMessage(packet)
	}
	if *probe.Version != ProtocolV1 {
		return UdpMessage{}, fmt.Errorf("expected JSON protocol version 0 or 1, received %d", *probe.Version)
	}
	return decodeV1WireMessage(packet)
}

func decodeLegacyWireMessage(packet []byte) (UdpMessage, error) {
	var wire legacyWireMessage
	err := json.Unmarshal(packet, &wire)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("decode legacy UDP message: %w", err)
	}
	return UdpMessage{Data: wire.Data, Address: wire.Address, Port: wire.Port, Sequence: wire.Sequence, Type: wire.Type, Cached: wire.Cached, Version: ProtocolLegacy}, nil
}

func decodeV1WireMessage(packet []byte) (UdpMessage, error) {
	var wire v1WireMessage
	decoder := json.NewDecoder(bytes.NewReader(packet))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&wire)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("decode protocol v1 UDP message: %w", err)
	}
	err = requireJSONEnd(decoder)
	if err != nil {
		return UdpMessage{}, err
	}
	message := UdpMessage{Data: wire.Data, Sequence: wire.Sequence, Version: wire.Version, SessionID: wire.SessionID, MessageID: wire.MessageID, Signature: wire.Auth}
	if wire.Kind == "ack" {
		message.Type = ackType
	} else if wire.Kind != "data" {
		return UdpMessage{}, fmt.Errorf("decode protocol v1 UDP message: expected kind data or ack, received %q", wire.Kind)
	}
	err = validateVersionedMessage(message)
	if err != nil {
		return UdpMessage{}, err
	}
	return message, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing struct{}
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return fmt.Errorf("decode protocol v1 UDP message: expected one JSON value, received trailing value")
	}
	return fmt.Errorf("decode protocol v1 UDP message trailing data: %w", err)
}

func v1MessageForWire(message UdpMessage) (v1WireMessage, error) {
	unsigned, err := makeWireEnvelope(message)
	if err != nil {
		return v1WireMessage{}, err
	}
	return v1WireMessage{Version: unsigned.Version, Kind: unsigned.Kind, SessionID: unsigned.SessionID, MessageID: unsigned.MessageID, Sequence: unsigned.Sequence, Data: unsigned.Data, Auth: message.Signature}, nil
}

func makeWireEnvelope(message UdpMessage) (wireEnvelope, error) {
	err := validateVersionedMessage(message)
	if err != nil {
		return wireEnvelope{}, err
	}
	kind := "data"
	if message.Type == ackType {
		kind = "ack"
	}
	return wireEnvelope{Version: message.Version, Kind: kind, SessionID: message.SessionID, MessageID: message.MessageID, Sequence: message.Sequence, Data: message.Data}, nil
}

func validateVersionedMessage(message UdpMessage) error {
	if message.Version != ProtocolV1 && message.Version != ProtocolV2 {
		return fmt.Errorf("validate protocol v%d UDP message failed: expected version %d or %d", message.Version, ProtocolV1, ProtocolV2)
	}
	if !validWireID(string(message.SessionID)) {
		return fmt.Errorf("validate protocol v%d UDP message failed: expected session id as 32 lowercase hexadecimal characters, received %q", message.Version, message.SessionID)
	}
	if !validWireID(string(message.MessageID)) {
		return fmt.Errorf("validate protocol v%d UDP message failed: expected message id as 32 lowercase hexadecimal characters, received %q", message.Version, message.MessageID)
	}
	if message.Type != "" && message.Type != ackType {
		return fmt.Errorf("validate protocol v%d UDP message %q failed: expected empty type or %q, received %q", message.Version, message.MessageID, ackType, message.Type)
	}
	if message.Type == ackType && len(message.Data) != 0 {
		return fmt.Errorf("validate protocol v%d acknowledgement %q failed: expected empty data, received %d bytes", message.Version, message.MessageID, len(message.Data))
	}
	return nil
}

func validWireID(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func legacyMessageForWire(message UdpMessage) legacyWireMessage {
	return legacyWireMessage{Data: message.Data, Address: message.Address, Port: message.Port, Sequence: message.Sequence, Type: message.Type, Cached: message.Cached}
}

// Binary (version 2) packet layout, big-endian:
//
//	offset  0: magic 0xB2
//	offset  1: protocol version, 2
//	offset  2: kind, 1 data or 2 ack
//	offset  3: flags, bit 0 set when a signature trailer is present
//	offset  4: session ID, 16 raw bytes
//	offset 20: message ID, 16 raw bytes
//	offset 36: sequence, uint64
//	offset 44: payload until the end of the packet
//	trailer  : 32-byte HMAC-SHA256 when the signed flag is set
//
// The magic byte cannot collide with JSON packets, which start with '{', or
// with STUN packets, whose first two bits are zero.
const (
	binaryWireMagic     byte = 0xB2
	binaryWireVersion   byte = 2
	binaryKindData      byte = 1
	binaryKindAck       byte = 2
	binaryFlagSigned    byte = 0x01
	binaryHeaderSize         = 44
	binarySignatureSize      = sha256.Size
)

func isBinaryWirePacket(packet []byte) bool {
	return len(packet) > 0 && packet[0] == binaryWireMagic
}

func encodeBinaryWireMessage(message UdpMessage) ([]byte, error) {
	err := validateVersionedMessage(message)
	if err != nil {
		return nil, err
	}
	if message.Sequence < 0 {
		return nil, fmt.Errorf("encode binary UDP message %q failed: expected sequence zero or greater, received %d", message.MessageID, message.Sequence)
	}
	sessionID, err := hex.DecodeString(string(message.SessionID))
	if err != nil {
		return nil, fmt.Errorf("encode binary UDP message %q session id: %w", message.MessageID, err)
	}
	messageID, err := hex.DecodeString(string(message.MessageID))
	if err != nil {
		return nil, fmt.Errorf("encode binary UDP message %q message id: %w", message.MessageID, err)
	}

	kind := binaryKindData
	if message.Type == ackType {
		kind = binaryKindAck
	}
	flags := byte(0)
	var signature []byte
	if message.Signature != "" {
		signature, err = base64.RawURLEncoding.DecodeString(message.Signature)
		if err != nil {
			return nil, fmt.Errorf("encode binary UDP message %q signature: %w", message.MessageID, err)
		}
		if len(signature) != binarySignatureSize {
			return nil, fmt.Errorf("encode binary UDP message %q failed: expected %d signature bytes, received %d", message.MessageID, binarySignatureSize, len(signature))
		}
		flags = flags | binaryFlagSigned
	}

	packet := make([]byte, binaryHeaderSize, binaryHeaderSize+len(message.Data)+len(signature))
	packet[0] = binaryWireMagic
	packet[1] = binaryWireVersion
	packet[2] = kind
	packet[3] = flags
	copy(packet[4:20], sessionID)
	copy(packet[20:36], messageID)
	binary.BigEndian.PutUint64(packet[36:44], uint64(message.Sequence))
	packet = append(packet, message.Data...)
	packet = append(packet, signature...)
	return packet, nil
}

func decodeBinaryWireMessage(packet []byte) (UdpMessage, error) {
	if len(packet) < binaryHeaderSize {
		return UdpMessage{}, fmt.Errorf("decode binary UDP message: expected at least %d header bytes, received %d", binaryHeaderSize, len(packet))
	}
	if packet[1] != binaryWireVersion {
		return UdpMessage{}, fmt.Errorf("decode binary UDP message: expected binary protocol version %d, received %d", binaryWireVersion, packet[1])
	}
	if packet[3]&^binaryFlagSigned != 0 {
		return UdpMessage{}, fmt.Errorf("decode binary UDP message: expected flags zero or %#02x, received %#02x", binaryFlagSigned, packet[3])
	}

	message := UdpMessage{Version: ProtocolV2, SessionID: SessionID(hex.EncodeToString(packet[4:20])), MessageID: MessageID(hex.EncodeToString(packet[20:36]))}
	if packet[2] == binaryKindAck {
		message.Type = ackType
	} else if packet[2] != binaryKindData {
		return UdpMessage{}, fmt.Errorf("decode binary UDP message %q: expected kind %d or %d, received %d", message.MessageID, binaryKindData, binaryKindAck, packet[2])
	}

	sequence := binary.BigEndian.Uint64(packet[36:44])
	if sequence > uint64(math.MaxInt) {
		return UdpMessage{}, fmt.Errorf("decode binary UDP message %q: expected sequence at most %d, received %d", message.MessageID, math.MaxInt, sequence)
	}
	message.Sequence = int(sequence)

	body := packet[binaryHeaderSize:]
	if packet[3]&binaryFlagSigned != 0 {
		if len(body) < binarySignatureSize {
			return UdpMessage{}, fmt.Errorf("decode binary UDP message %q: expected %d trailing signature bytes, received %d body bytes", message.MessageID, binarySignatureSize, len(body))
		}
		message.Signature = base64.RawURLEncoding.EncodeToString(body[len(body)-binarySignatureSize:])
		body = body[:len(body)-binarySignatureSize]
	}
	if len(body) > 0 {
		message.Data = append([]byte(nil), body...)
	}

	err := validateVersionedMessage(message)
	if err != nil {
		return UdpMessage{}, err
	}
	return message, nil
}
