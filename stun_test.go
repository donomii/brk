package brk

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestResolveSTUNConfigUsesDefaults(t *testing.T) {
	config, err := ResolveSTUNConfig(STUNConfig{})
	if err != nil {
		t.Fatalf("resolve STUN config failed: expected nil error, received %v", err)
	}
	if config.Server != DefaultSTUNServer {
		t.Fatalf("STUN server mismatch: expected %q, received %q", DefaultSTUNServer, config.Server)
	}
	if config.Timeout != 3*time.Second {
		t.Fatalf("STUN timeout mismatch: expected %v, received %v", 3*time.Second, config.Timeout)
	}
}

func TestResolveSTUNConfigRejectsInvalidTimeout(t *testing.T) {
	_, err := ResolveSTUNConfig(STUNConfig{Timeout: -1})
	if err == nil {
		t.Fatalf("negative STUN timeout mismatch: expected error, received nil")
	}
}

func TestMakeSTUNBindingRequest(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}

	request := makeSTUNBindingRequest(transactionID)

	if binary.BigEndian.Uint16(request[0:2]) != 0x0001 {
		t.Fatalf("STUN request type mismatch: expected 0x0001, received 0x%04x", binary.BigEndian.Uint16(request[0:2]))
	}
	if binary.BigEndian.Uint16(request[2:4]) != 0 {
		t.Fatalf("STUN request length mismatch: expected %d, received %d", 0, binary.BigEndian.Uint16(request[2:4]))
	}
	if binary.BigEndian.Uint32(request[4:8]) != stunMagicCookie {
		t.Fatalf("STUN request cookie mismatch: expected 0x%08x, received 0x%08x", stunMagicCookie, binary.BigEndian.Uint32(request[4:8]))
	}
	for index, value := range transactionID {
		if request[8+index] != value {
			t.Fatalf("STUN transaction mismatch at byte %d: expected %d, received %d", index, value, request[8+index])
		}
	}
}

func TestParseSTUNBindingResponseXORIPv4(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	packet := makeSTUNXORIPv4Response(transactionID, "203.0.113.9", 54321)

	address, err := parseSTUNBindingResponse(packet, transactionID, "stun.example:3478")
	if err != nil {
		t.Fatalf("parse STUN response failed: expected nil error, received %v", err)
	}

	if address.Server != "stun.example:3478" {
		t.Fatalf("STUN address server mismatch: expected %q, received %q", "stun.example:3478", address.Server)
	}
	if address.IP != "203.0.113.9" {
		t.Fatalf("STUN address IP mismatch: expected %q, received %q", "203.0.113.9", address.IP)
	}
	if address.Port != 54321 {
		t.Fatalf("STUN address port mismatch: expected %d, received %d", 54321, address.Port)
	}
	if address.Family != "IPv4" {
		t.Fatalf("STUN address family mismatch: expected %q, received %q", "IPv4", address.Family)
	}
}

func makeSTUNXORIPv4Response(transactionID [12]byte, ip string, port int) []byte {
	parsedIP := net.ParseIP(ip).To4()
	packet := make([]byte, 32)
	binary.BigEndian.PutUint16(packet[0:2], 0x0101)
	binary.BigEndian.PutUint16(packet[2:4], 12)
	binary.BigEndian.PutUint32(packet[4:8], stunMagicCookie)
	copy(packet[8:20], transactionID[:])
	binary.BigEndian.PutUint16(packet[20:22], 0x0020)
	binary.BigEndian.PutUint16(packet[22:24], 8)
	packet[24] = 0
	packet[25] = 0x01
	binary.BigEndian.PutUint16(packet[26:28], uint16(port)^uint16(stunMagicCookie>>16))
	packet[28] = parsedIP[0] ^ 0x21
	packet[29] = parsedIP[1] ^ 0x12
	packet[30] = parsedIP[2] ^ 0xA4
	packet[31] = parsedIP[3] ^ 0x42
	return packet
}
