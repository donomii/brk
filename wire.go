package brk

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

type v1UnsignedMessage struct {
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
	wire, err := unsignedV1WireMessage(message)
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
	} else {
		return nil, fmt.Errorf("encode UDP message failed: expected protocol version 0 or 1, received %d", message.Version)
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
		return UdpMessage{}, fmt.Errorf("expected protocol version 0 or 1, received %d", *probe.Version)
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
	err = validateV1Message(message)
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
	unsigned, err := unsignedV1WireMessage(message)
	if err != nil {
		return v1WireMessage{}, err
	}
	return v1WireMessage{Version: unsigned.Version, Kind: unsigned.Kind, SessionID: unsigned.SessionID, MessageID: unsigned.MessageID, Sequence: unsigned.Sequence, Data: unsigned.Data, Auth: message.Signature}, nil
}

func unsignedV1WireMessage(message UdpMessage) (v1UnsignedMessage, error) {
	err := validateV1Message(message)
	if err != nil {
		return v1UnsignedMessage{}, err
	}
	kind := "data"
	if message.Type == ackType {
		kind = "ack"
	}
	return v1UnsignedMessage{Version: ProtocolV1, Kind: kind, SessionID: message.SessionID, MessageID: message.MessageID, Sequence: message.Sequence, Data: message.Data}, nil
}

func validateV1Message(message UdpMessage) error {
	if message.Version != ProtocolV1 {
		return fmt.Errorf("validate protocol v1 UDP message failed: expected version %d, received %d", ProtocolV1, message.Version)
	}
	if !validWireID(string(message.SessionID)) {
		return fmt.Errorf("validate protocol v1 UDP message failed: expected session id as 32 lowercase hexadecimal characters, received %q", message.SessionID)
	}
	if !validWireID(string(message.MessageID)) {
		return fmt.Errorf("validate protocol v1 UDP message failed: expected message id as 32 lowercase hexadecimal characters, received %q", message.MessageID)
	}
	if message.Type != "" && message.Type != ackType {
		return fmt.Errorf("validate protocol v1 UDP message %q failed: expected empty type or %q, received %q", message.MessageID, ackType, message.Type)
	}
	if message.Type == ackType && len(message.Data) != 0 {
		return fmt.Errorf("validate protocol v1 acknowledgement %q failed: expected empty data, received %d bytes", message.MessageID, len(message.Data))
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
