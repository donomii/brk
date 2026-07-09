package brk

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const stunMagicCookie uint32 = 0x2112A442

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
