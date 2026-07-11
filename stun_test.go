package brk

import (
	"encoding/binary"
	"fmt"
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

func TestDiscoverExternalAddressUsesConfiguredServer(t *testing.T) {
	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("start local STUN server failed: expected nil error, received %v", err)
	}
	defer func() {
		if closeErr := server.Close(); closeErr != nil {
			t.Fatalf("close local STUN server failed: expected nil error, received %v", closeErr)
		}
	}()
	err = server.SetDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("set local STUN deadline failed: expected nil error, received %v", err)
	}

	responseResult := make(chan error, 1)
	go serveOneSTUNResponse(server, responseResult)

	address, err := DiscoverExternalAddress(STUNConfig{Server: server.LocalAddr().String(), Timeout: time.Second})
	if err != nil {
		t.Fatalf("discover external address failed: expected nil error, received %v", err)
	}
	if responseErr := <-responseResult; responseErr != nil {
		t.Fatalf("serve local STUN response failed: expected nil error, received %v", responseErr)
	}
	if address.Server != server.LocalAddr().String() {
		t.Fatalf("discovered server mismatch: expected %q, received %q", server.LocalAddr().String(), address.Server)
	}
	if address.IP != "203.0.113.9" || address.Port != 54321 || address.Family != "IPv4" {
		t.Fatalf("discovered address mismatch: expected 203.0.113.9:54321 IPv4, received %+v", address)
	}
}

func serveOneSTUNResponse(server *net.UDPConn, result chan<- error) {
	request := make([]byte, 1500)
	n, client, err := server.ReadFromUDP(request)
	if err != nil {
		result <- fmt.Errorf("read binding request: %w", err)
		return
	}
	if n != 20 {
		result <- fmt.Errorf("binding request size mismatch: expected %d bytes, received %d", 20, n)
		return
	}
	if binary.BigEndian.Uint16(request[0:2]) != 0x0001 || binary.BigEndian.Uint32(request[4:8]) != stunMagicCookie {
		result <- fmt.Errorf("binding request header mismatch: type=0x%04x cookie=0x%08x", binary.BigEndian.Uint16(request[0:2]), binary.BigEndian.Uint32(request[4:8]))
		return
	}
	var transactionID [12]byte
	copy(transactionID[:], request[8:20])
	_, err = server.WriteToUDP(makeSTUNXORIPv4Response(transactionID, "203.0.113.9", 54321), client)
	if err != nil {
		result <- fmt.Errorf("write binding response: %w", err)
		return
	}
	result <- nil
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

func TestParseSTUNBindingResponseMappedIPv4(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	packet := makeSTUNMappedIPv4Response(transactionID, "198.51.100.4", 4567)

	address, err := parseSTUNBindingResponse(packet, transactionID, "stun.example:3478")
	if err != nil {
		t.Fatalf("parse mapped STUN response failed: expected nil error, received %v", err)
	}
	if address.IP != "198.51.100.4" || address.Port != 4567 || address.Family != "IPv4" {
		t.Fatalf("mapped STUN address mismatch: expected 198.51.100.4:4567 IPv4, received %+v", address)
	}
}

func TestParseSTUNBindingResponseRejectsMalformedPackets(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	validPacket := makeSTUNXORIPv4Response(transactionID, "203.0.113.9", 54321)
	wrongType := append([]byte(nil), validPacket...)
	binary.BigEndian.PutUint16(wrongType[0:2], 0x0111)
	wrongCookie := append([]byte(nil), validPacket...)
	binary.BigEndian.PutUint32(wrongCookie[4:8], 0)
	wrongTransaction := append([]byte(nil), validPacket...)
	wrongTransaction[8] = wrongTransaction[8] + 1
	shortAttributes := append([]byte(nil), validPacket...)
	binary.BigEndian.PutUint16(shortAttributes[2:4], 13)
	oversizedAttribute := append([]byte(nil), validPacket...)
	binary.BigEndian.PutUint16(oversizedAttribute[22:24], 20)
	missingAddress := append([]byte(nil), validPacket...)
	binary.BigEndian.PutUint16(missingAddress[20:22], 0x0006)
	tests := []struct {
		name   string
		packet []byte
	}{
		{name: "short header", packet: validPacket[:19]},
		{name: "wrong response type", packet: wrongType},
		{name: "wrong cookie", packet: wrongCookie},
		{name: "wrong transaction", packet: wrongTransaction},
		{name: "short attributes", packet: shortAttributes},
		{name: "oversized attribute", packet: oversizedAttribute},
		{name: "missing mapped address", packet: missingAddress},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseSTUNBindingResponse(test.packet, transactionID, "stun.example:3478")
			if err == nil {
				t.Fatalf("malformed STUN packet mismatch: expected error, received nil")
			}
		})
	}
}

func TestParseSTUNBindingResponseXORIPv6(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	ip := net.ParseIP("2001:db8::9").To16()
	port := 54321

	mask := make([]byte, 16)
	binary.BigEndian.PutUint32(mask[0:4], stunMagicCookie)
	copy(mask[4:16], transactionID[:])
	value := make([]byte, 20)
	value[1] = 0x02
	binary.BigEndian.PutUint16(value[2:4], uint16(port)^uint16(stunMagicCookie>>16))
	for index := 0; index < 16; index++ {
		value[4+index] = ip[index] ^ mask[index]
	}
	packet := makeSTUNResponse(transactionID, append([]byte{0x00, 0x20, 0x00, 20}, value...))

	address, err := parseSTUNBindingResponse(packet, transactionID, "stun.example:3478")
	if err != nil {
		t.Fatalf("parse STUN response failed: expected nil error, received %v", err)
	}
	if address.IP != "2001:db8::9" || address.Port != port || address.Family != "IPv6" {
		t.Fatalf("STUN IPv6 address mismatch: expected 2001:db8::9:%d IPv6, received %+v", port, address)
	}
}

func TestParseSTUNBindingResponseSkipsUnknownAttributes(t *testing.T) {
	transactionID := [12]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	software := []byte("hello")
	attributes := make([]byte, 0, 24)
	attributes = append(attributes, 0x80, 0x22, 0x00, byte(len(software)))
	attributes = append(attributes, software...)
	attributes = append(attributes, 0, 0, 0)
	attributes = append(attributes, makeSTUNXORIPv4Response(transactionID, "203.0.113.9", 54321)[20:]...)
	packet := makeSTUNResponse(transactionID, attributes)

	address, err := parseSTUNBindingResponse(packet, transactionID, "stun.example:3478")
	if err != nil {
		t.Fatalf("parse STUN response failed: expected nil error, received %v", err)
	}
	if address.IP != "203.0.113.9" || address.Port != 54321 {
		t.Fatalf("STUN address mismatch after odd-length attribute skip: expected 203.0.113.9:54321, received %+v", address)
	}
}

func makeSTUNResponse(transactionID [12]byte, attributes []byte) []byte {
	packet := make([]byte, 20+len(attributes))
	binary.BigEndian.PutUint16(packet[0:2], 0x0101)
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(attributes)))
	binary.BigEndian.PutUint32(packet[4:8], stunMagicCookie)
	copy(packet[8:20], transactionID[:])
	copy(packet[20:], attributes)
	return packet
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

func makeSTUNMappedIPv4Response(transactionID [12]byte, ip string, port int) []byte {
	parsedIP := net.ParseIP(ip).To4()
	packet := make([]byte, 32)
	binary.BigEndian.PutUint16(packet[0:2], 0x0101)
	binary.BigEndian.PutUint16(packet[2:4], 12)
	binary.BigEndian.PutUint32(packet[4:8], stunMagicCookie)
	copy(packet[8:20], transactionID[:])
	binary.BigEndian.PutUint16(packet[20:22], 0x0001)
	binary.BigEndian.PutUint16(packet[22:24], 8)
	packet[24] = 0
	packet[25] = 0x01
	binary.BigEndian.PutUint16(packet[26:28], uint16(port))
	copy(packet[28:32], parsedIP)
	return packet
}
