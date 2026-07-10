package brk

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// Endpoint returns the message's validated target or source endpoint.
func (message UdpMessage) Endpoint() (netip.AddrPort, error) {
	return endpointFromParts(message.Address, message.Port, "UDP message")
}

// Endpoint returns the validated endpoint reported by STUN.
func (address ExternalAddress) Endpoint() (netip.AddrPort, error) {
	return endpointFromParts(address.IP, address.Port, "external address")
}

// SendMessageTo validates target, copies data, and queues a message without a delivery receipt.
func SendMessageTo(outgoing chan UdpMessage, data []byte, target netip.AddrPort) error {
	target, err := validateEndpoint(target, "send UDP message")
	if err != nil {
		return err
	}
	SendMessage(outgoing, data, target.Addr().String(), int(target.Port()))
	return nil
}

// LocalEndpoint returns the normalized endpoint bound by the plain UDP server.
func (server *UdpServer) LocalEndpoint() netip.AddrPort {
	return server.localEndpoint
}

// LocalEndpoint returns the normalized endpoint bound by the retry server.
func (server *RetryUdpServer) LocalEndpoint() netip.AddrPort {
	return server.Network.LocalEndpoint()
}

func endpointFromParts(address string, port int, operation string) (netip.AddrPort, error) {
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%s endpoint invalid: parse address %q: %w", operation, address, err)
	}
	if port < 1 || port > 65535 {
		return netip.AddrPort{}, fmt.Errorf("%s endpoint invalid: expected port in 1..65535, received %d", operation, port)
	}
	return normalizeEndpoint(netip.AddrPortFrom(parsed, uint16(port))), nil
}

func validateEndpoint(endpoint netip.AddrPort, operation string) (netip.AddrPort, error) {
	endpoint = normalizeEndpoint(endpoint)
	if !endpoint.IsValid() {
		return netip.AddrPort{}, fmt.Errorf("%s endpoint invalid: expected valid IP address and port, received %v", operation, endpoint)
	}
	if endpoint.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("%s endpoint invalid: expected nonzero port, received %d", operation, endpoint.Port())
	}
	return endpoint, nil
}

func normalizeEndpoint(endpoint netip.AddrPort) netip.AddrPort {
	if !endpoint.IsValid() {
		return endpoint
	}
	return netip.AddrPortFrom(endpoint.Addr().Unmap(), endpoint.Port())
}

func networkForHost(host string) string {
	parsed := net.ParseIP(host)
	if parsed != nil && parsed.To4() == nil {
		return "udp6"
	}
	return "udp4"
}

func resolveCompatibleEndpoint(address string, local netip.AddrPort) (netip.AddrPort, error) {
	resolved, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("resolve UDP endpoint %q: %w", address, err)
	}
	endpoint := normalizeEndpoint(resolved.AddrPort())
	if !endpoint.IsValid() || endpoint.Port() == 0 {
		return netip.AddrPort{}, fmt.Errorf("resolve UDP endpoint %q: expected valid address and nonzero port, received %v", address, endpoint)
	}
	if local.Addr().Is4() != endpoint.Addr().Is4() {
		return netip.AddrPort{}, fmt.Errorf("resolve UDP endpoint %q: address family %s does not match local endpoint %v", address, addressFamily(endpoint.Addr()), local)
	}
	return endpoint, nil
}

func addressFamily(address netip.Addr) string {
	if address.Is4() {
		return "IPv4"
	}
	return "IPv6"
}

func endpointString(address string, port int) string {
	return net.JoinHostPort(address, strconv.Itoa(port))
}
