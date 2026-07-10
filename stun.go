package brk

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"
)

const (
	stunMagicCookie       uint32 = 0x2112A442
	stunBindingRequest    uint16 = 0x0001
	stunBindingIndication uint16 = 0x0011
	stunBindingSuccess    uint16 = 0x0101
	stunBindingError      uint16 = 0x0111
	stunSoftwareAttribute uint16 = 0x8022
	brkSTUNSoftware              = "brk"
)

// DiscoverExternalAddress reports the mapping observed for a temporary UDP socket. The socket is closed before this
// function returns, so the result does not advertise an existing UdpServer.
func DiscoverExternalAddress(config STUNConfig) (address ExternalAddress, err error) {
	resolved, err := ResolveSTUNConfig(config)
	if err != nil {
		return ExternalAddress{}, err
	}

	var transactionID [12]byte
	_, err = rand.Read(transactionID[:])
	if err != nil {
		return ExternalAddress{}, fmt.Errorf("create STUN transaction id: %w", err)
	}

	conn, err := net.DialTimeout("udp", resolved.Server, resolved.Timeout)
	if err != nil {
		return ExternalAddress{}, fmt.Errorf("connect to STUN server %q: %w", resolved.Server, err)
	}
	defer func() {
		closeErr := conn.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close STUN connection to %q: %w", resolved.Server, closeErr)
		}
	}()

	deadline := time.Now().Add(resolved.Timeout)
	err = conn.SetDeadline(deadline)
	if err != nil {
		return ExternalAddress{}, fmt.Errorf("set STUN deadline for server %q: %w", resolved.Server, err)
	}

	request := makeSTUNBindingRequest(transactionID)
	_, err = conn.Write(request)
	if err != nil {
		return ExternalAddress{}, fmt.Errorf("send STUN binding request to %q: %w", resolved.Server, err)
	}

	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return ExternalAddress{}, fmt.Errorf("read STUN binding response from %q: %w", resolved.Server, err)
	}

	return parseSTUNBindingResponse(buffer[:n], transactionID, resolved.Server)
}

func makeSTUNBindingRequest(transactionID [12]byte) []byte {
	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request[0:2], 0x0001)
	binary.BigEndian.PutUint16(request[2:4], 0)
	binary.BigEndian.PutUint32(request[4:8], stunMagicCookie)
	copy(request[8:20], transactionID[:])
	return request
}

// DiscoverExternalAddress reports the mapping observed for the live plain server socket.
func (server *UdpServer) DiscoverExternalAddress(ctx context.Context, config STUNConfig) (ExternalAddress, error) {
	if ctx == nil {
		return ExternalAddress{}, fmt.Errorf("discover live external address failed: expected non-nil context")
	}
	resolved, err := ResolveSTUNConfig(config)
	if err != nil {
		return ExternalAddress{}, err
	}
	destination, err := resolveCompatibleEndpoint(resolved.Server, server.LocalEndpoint())
	if err != nil {
		return ExternalAddress{}, fmt.Errorf("discover live external address from %v failed: %w", server.LocalEndpoint(), err)
	}
	transactionID, err := newSTUNTransactionID()
	if err != nil {
		return ExternalAddress{}, err
	}
	response, err := server.exchangeSTUN(ctx, destination, makeSTUNBindingRequest(transactionID), transactionID, resolved.Timeout, "send live STUN binding request")
	if err != nil {
		return ExternalAddress{}, err
	}
	return parseSTUNBindingResponse(response.Packet, transactionID, resolved.Server)
}

// DiscoverExternalAddress reports the mapping observed for the retry server's live socket.
func (server *RetryUdpServer) DiscoverExternalAddress(ctx context.Context, config STUNConfig) (ExternalAddress, error) {
	return server.Network.DiscoverExternalAddress(ctx, config)
}

func (server *UdpServer) exchangeSTUN(ctx context.Context, destination netip.AddrPort, packet []byte, transactionID [12]byte, timeout time.Duration, operation string) (receivedDatagram, error) {
	server.stunLock.Lock()
	defer server.stunLock.Unlock()
	drainSTUNResponses(server.stunResponses)
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := server.sendControlDatagram(requestCtx, packet, destination, operation)
	if err != nil {
		return receivedDatagram{}, err
	}
	for {
		select {
		case response := <-server.stunResponses:
			if response.Source == destination && stunTransactionMatches(response.Packet, transactionID) {
				return response, nil
			}
		case <-requestCtx.Done():
			return receivedDatagram{}, fmt.Errorf("%s to %v failed after %v: %w", operation, destination, timeout, requestCtx.Err())
		case <-server.Done():
			return receivedDatagram{}, fmt.Errorf("%s to %v failed: UDP server %v closed", operation, destination, server.LocalEndpoint())
		}
	}
}

func (server *UdpServer) sendControlDatagram(ctx context.Context, packet []byte, destination netip.AddrPort, operation string) error {
	result := make(chan error, 1)
	datagram := outboundDatagram{Packet: packet, Destination: destination, Result: result, Operation: operation}
	select {
	case server.controlOutgoing <- datagram:
	case <-ctx.Done():
		return fmt.Errorf("%s to %v failed before queueing: %w", operation, destination, ctx.Err())
	case <-server.Done():
		return fmt.Errorf("%s to %v failed: UDP server %v closed", operation, destination, server.LocalEndpoint())
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%s to %v failed while writing: %w", operation, destination, ctx.Err())
	case <-server.Done():
		return fmt.Errorf("%s to %v failed while writing: UDP server %v closed", operation, destination, server.LocalEndpoint())
	}
}

func (server *UdpServer) handleSTUNPacket(ctx context.Context, packet []byte, source netip.AddrPort) error {
	err := validateSTUNPacket(packet)
	if err != nil {
		return err
	}
	messageType := binary.BigEndian.Uint16(packet[0:2])
	if messageType == stunBindingSuccess || messageType == stunBindingError {
		select {
		case server.stunResponses <- receivedDatagram{Packet: append([]byte(nil), packet...), Source: source}:
			return nil
		default:
			return fmt.Errorf("route STUN response from %v failed: response queue is full", source)
		}
	}
	if messageType == stunBindingRequest && hasSTUNSoftware(packet, brkSTUNSoftware) {
		transactionID := stunTransactionID(packet)
		response := makeSTUNBindingSuccess(transactionID, source)
		result := make(chan error, 1)
		select {
		case server.controlOutgoing <- outboundDatagram{Packet: response, Destination: source, Result: result, Operation: "send peer STUN response"}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if messageType == stunBindingIndication && hasSTUNSoftware(packet, brkSTUNSoftware) {
		return nil
	}
	return fmt.Errorf("handle STUN packet from %v failed: unsupported message type 0x%04x", source, messageType)
}

func isSTUNPacket(packet []byte) bool {
	return len(packet) >= 8 && packet[0]&0xc0 == 0 && binary.BigEndian.Uint32(packet[4:8]) == stunMagicCookie
}

func validateSTUNPacket(packet []byte) error {
	if len(packet) < 20 {
		return fmt.Errorf("validate STUN packet failed: expected at least 20 bytes, received %d", len(packet))
	}
	messageLength := int(binary.BigEndian.Uint16(packet[2:4]))
	if messageLength%4 != 0 {
		return fmt.Errorf("validate STUN packet failed: expected attribute length divisible by four, received %d", messageLength)
	}
	if len(packet) != 20+messageLength {
		return fmt.Errorf("validate STUN packet failed: expected %d total bytes, received %d", 20+messageLength, len(packet))
	}
	return nil
}

func newSTUNTransactionID() ([12]byte, error) {
	var transactionID [12]byte
	_, err := rand.Read(transactionID[:])
	if err != nil {
		return [12]byte{}, fmt.Errorf("create STUN transaction id: %w", err)
	}
	return transactionID, nil
}

func stunTransactionID(packet []byte) [12]byte {
	var transactionID [12]byte
	if len(packet) >= 20 {
		copy(transactionID[:], packet[8:20])
	}
	return transactionID
}

func stunTransactionMatches(packet []byte, transactionID [12]byte) bool {
	return len(packet) >= 20 && bytes.Equal(packet[8:20], transactionID[:])
}

func drainSTUNResponses(responses chan receivedDatagram) {
	for {
		select {
		case <-responses:
		default:
			return
		}
	}
}

func makeSTUNPeerRequest(transactionID [12]byte) []byte {
	return makeMarkedSTUNMessage(stunBindingRequest, transactionID)
}

func makeSTUNBindingIndication(transactionID [12]byte) []byte {
	return makeMarkedSTUNMessage(stunBindingIndication, transactionID)
}

func makeMarkedSTUNMessage(messageType uint16, transactionID [12]byte) []byte {
	software := []byte(brkSTUNSoftware)
	attributeLength := 4 + len(software) + stunPadding(len(software))
	packet := make([]byte, 20+attributeLength)
	binary.BigEndian.PutUint16(packet[0:2], messageType)
	binary.BigEndian.PutUint16(packet[2:4], uint16(attributeLength))
	binary.BigEndian.PutUint32(packet[4:8], stunMagicCookie)
	copy(packet[8:20], transactionID[:])
	binary.BigEndian.PutUint16(packet[20:22], stunSoftwareAttribute)
	binary.BigEndian.PutUint16(packet[22:24], uint16(len(software)))
	copy(packet[24:24+len(software)], software)
	return packet
}

func hasSTUNSoftware(packet []byte, expected string) bool {
	if len(packet) < 20 {
		return false
	}
	attributes := packet[20:]
	for offset := 0; offset+4 <= len(attributes); {
		attributeType := binary.BigEndian.Uint16(attributes[offset : offset+2])
		attributeLength := int(binary.BigEndian.Uint16(attributes[offset+2 : offset+4]))
		valueStart := offset + 4
		valueEnd := valueStart + attributeLength
		if valueEnd > len(attributes) {
			return false
		}
		if attributeType == stunSoftwareAttribute && string(attributes[valueStart:valueEnd]) == expected {
			return true
		}
		offset = valueEnd + stunPadding(attributeLength)
	}
	return false
}

func makeSTUNBindingSuccess(transactionID [12]byte, endpoint netip.AddrPort) []byte {
	value := makeXORMappedAddressValue(transactionID, endpoint)
	attributeLength := 4 + len(value)
	packet := make([]byte, 20+attributeLength)
	binary.BigEndian.PutUint16(packet[0:2], stunBindingSuccess)
	binary.BigEndian.PutUint16(packet[2:4], uint16(attributeLength))
	binary.BigEndian.PutUint32(packet[4:8], stunMagicCookie)
	copy(packet[8:20], transactionID[:])
	binary.BigEndian.PutUint16(packet[20:22], 0x0020)
	binary.BigEndian.PutUint16(packet[22:24], uint16(len(value)))
	copy(packet[24:], value)
	return packet
}

func makeXORMappedAddressValue(transactionID [12]byte, endpoint netip.AddrPort) []byte {
	endpoint = normalizeEndpoint(endpoint)
	if endpoint.Addr().Is4() {
		value := make([]byte, 8)
		value[1] = 0x01
		binary.BigEndian.PutUint16(value[2:4], endpoint.Port()^uint16(stunMagicCookie>>16))
		address := endpoint.Addr().As4()
		cookie := [4]byte{0x21, 0x12, 0xa4, 0x42}
		for index := 0; index < len(address); index++ {
			value[4+index] = address[index] ^ cookie[index]
		}
		return value
	}
	value := make([]byte, 20)
	value[1] = 0x02
	binary.BigEndian.PutUint16(value[2:4], endpoint.Port()^uint16(stunMagicCookie>>16))
	address := endpoint.Addr().As16()
	mask := [16]byte{0x21, 0x12, 0xa4, 0x42}
	copy(mask[4:], transactionID[:])
	for index := 0; index < len(address); index++ {
		value[4+index] = address[index] ^ mask[index]
	}
	return value
}

func parseSTUNBindingResponse(packet []byte, transactionID [12]byte, server string) (ExternalAddress, error) {
	if len(packet) < 20 {
		return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: expected at least 20 bytes, received %d", server, len(packet))
	}
	if binary.BigEndian.Uint16(packet[0:2]) != 0x0101 {
		return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: expected binding success response 0x0101, received 0x%04x", server, binary.BigEndian.Uint16(packet[0:2]))
	}
	if binary.BigEndian.Uint32(packet[4:8]) != stunMagicCookie {
		return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: expected magic cookie 0x%08x, received 0x%08x", server, stunMagicCookie, binary.BigEndian.Uint32(packet[4:8]))
	}
	if !bytes.Equal(packet[8:20], transactionID[:]) {
		return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: transaction id did not match request", server)
	}

	messageLength := int(binary.BigEndian.Uint16(packet[2:4]))
	if len(packet) < 20+messageLength {
		return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: expected %d attribute bytes, received %d", server, messageLength, len(packet)-20)
	}

	attributes := packet[20 : 20+messageLength]
	for offset := 0; offset+4 <= len(attributes); {
		attributeType := binary.BigEndian.Uint16(attributes[offset : offset+2])
		attributeLength := int(binary.BigEndian.Uint16(attributes[offset+2 : offset+4]))
		valueStart := offset + 4
		valueEnd := valueStart + attributeLength
		if valueEnd > len(attributes) {
			return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: attribute 0x%04x length %d exceeds packet", server, attributeType, attributeLength)
		}
		value := attributes[valueStart:valueEnd]
		if attributeType == 0x0020 {
			return parseXORMappedAddress(value, transactionID, server)
		} else if attributeType == 0x0001 {
			return parseMappedAddress(value, server)
		}
		offset = valueEnd + stunPadding(attributeLength)
	}

	return ExternalAddress{}, fmt.Errorf("parse STUN response from %q: expected XOR-MAPPED-ADDRESS or MAPPED-ADDRESS attribute", server)
}

func stunPadding(length int) int {
	remainder := length % 4
	if remainder == 0 {
		return 0
	} else {
		return 4 - remainder
	}
}

func parseXORMappedAddress(value []byte, transactionID [12]byte, server string) (ExternalAddress, error) {
	if len(value) < 4 {
		return ExternalAddress{}, fmt.Errorf("parse STUN XOR-MAPPED-ADDRESS from %q: expected at least 4 bytes, received %d", server, len(value))
	}

	family := value[1]
	port := int(binary.BigEndian.Uint16(value[2:4]) ^ uint16(stunMagicCookie>>16))
	if family == 0x01 {
		if len(value) < 8 {
			return ExternalAddress{}, fmt.Errorf("parse STUN IPv4 XOR-MAPPED-ADDRESS from %q: expected 8 bytes, received %d", server, len(value))
		}
		cookie := make([]byte, 4)
		binary.BigEndian.PutUint32(cookie, stunMagicCookie)
		ip := net.IPv4(value[4]^cookie[0], value[5]^cookie[1], value[6]^cookie[2], value[7]^cookie[3]).String()
		return ExternalAddress{Server: server, IP: ip, Port: port, Family: "IPv4"}, nil
	} else if family == 0x02 {
		if len(value) < 20 {
			return ExternalAddress{}, fmt.Errorf("parse STUN IPv6 XOR-MAPPED-ADDRESS from %q: expected 20 bytes, received %d", server, len(value))
		}
		mask := make([]byte, 16)
		binary.BigEndian.PutUint32(mask[0:4], stunMagicCookie)
		copy(mask[4:16], transactionID[:])
		ipBytes := make([]byte, 16)
		for i := 0; i < 16; i++ {
			ipBytes[i] = value[4+i] ^ mask[i]
		}
		return ExternalAddress{Server: server, IP: net.IP(ipBytes).String(), Port: port, Family: "IPv6"}, nil
	} else {
		return ExternalAddress{}, fmt.Errorf("parse STUN XOR-MAPPED-ADDRESS from %q: expected address family 0x01 or 0x02, received 0x%02x", server, family)
	}
}

func parseMappedAddress(value []byte, server string) (ExternalAddress, error) {
	if len(value) < 4 {
		return ExternalAddress{}, fmt.Errorf("parse STUN MAPPED-ADDRESS from %q: expected at least 4 bytes, received %d", server, len(value))
	}

	family := value[1]
	port := int(binary.BigEndian.Uint16(value[2:4]))
	if family == 0x01 {
		if len(value) < 8 {
			return ExternalAddress{}, fmt.Errorf("parse STUN IPv4 MAPPED-ADDRESS from %q: expected 8 bytes, received %d", server, len(value))
		}
		return ExternalAddress{Server: server, IP: net.IPv4(value[4], value[5], value[6], value[7]).String(), Port: port, Family: "IPv4"}, nil
	} else if family == 0x02 {
		if len(value) < 20 {
			return ExternalAddress{}, fmt.Errorf("parse STUN IPv6 MAPPED-ADDRESS from %q: expected 20 bytes, received %d", server, len(value))
		}
		return ExternalAddress{Server: server, IP: net.IP(value[4:20]).String(), Port: port, Family: "IPv6"}, nil
	} else {
		return ExternalAddress{}, fmt.Errorf("parse STUN MAPPED-ADDRESS from %q: expected address family 0x01 or 0x02, received 0x%02x", server, family)
	}
}
