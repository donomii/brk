package brk

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

var sequence int = makeInitialSequence()
var sequenceLock sync.Mutex

// Qlength is the legacy default channel capacity. New code should set RetryConfig.QueueLength.
var Qlength int = 2000

func makeInitialSequence() int {
	var value [4]byte
	_, err := rand.Read(value[:])
	if err != nil {
		panic(fmt.Sprintf("initialize UDP sequence failed: expected four random bytes, received error: %v", err))
	}
	return int(binary.BigEndian.Uint32(value[:]) & maxInitialSequence)
}

func getSequence() int {
	sequenceLock.Lock()
	defer sequenceLock.Unlock()
	sequence = sequence + 1
	return sequence
}

func startUdpContext(ctx context.Context, hostName, portNum string, queueLength int, stats *DeliveryStats, writeResults chan WriteResult, processor func(incoming, outgoing chan UdpMessage)) (*UdpServer, error) {
	if ctx == nil {
		return nil, fmt.Errorf("start UDP server failed: expected non-nil context")
	}
	if processor == nil {
		return nil, fmt.Errorf("start UDP server failed: expected non-nil processor")
	}
	if queueLength < 1 {
		return nil, fmt.Errorf("start UDP server failed: expected queue length greater than zero, received %d", queueLength)
	}

	service := net.JoinHostPort(hostName, portNum)
	network := networkForHost(hostName)
	udpAddr, err := net.ResolveUDPAddr(network, service)
	if err != nil {
		return nil, fmt.Errorf("resolve local UDP listen address %q: %w", service, err)
	}

	conn, err := net.ListenUDP(network, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen for UDP on %q: %w", service, err)
	}

	serverCtx, cancel := context.WithCancel(ctx)
	if writeResults == nil {
		writeResults = make(chan WriteResult, queueLength)
	}
	server := &UdpServer{
		Incoming:        make(chan UdpMessage, queueLength),
		Outgoing:        make(chan UdpMessage, queueLength),
		ctx:             serverCtx,
		localAddress:    conn.LocalAddr().String(),
		localEndpoint:   normalizeEndpoint(conn.LocalAddr().(*net.UDPAddr).AddrPort()),
		conn:            conn,
		stats:           stats,
		writeResults:    writeResults,
		controlOutgoing: make(chan outboundDatagram, queueLength),
		stunResponses:   make(chan receivedDatagram, queueLength),
		cancel:          cancel,
		done:            make(chan struct{}),
	}

	networkTasks := sync.WaitGroup{}
	networkTasks.Add(3)
	go func() {
		defer networkTasks.Done()
		closeUDPConnOnContext(serverCtx, conn)
	}()
	go func() {
		defer networkTasks.Done()
		udpWriter(serverCtx, conn, server.Outgoing, server.controlOutgoing, server.writeResults, stats)
	}()
	go processor(server.Incoming, server.Outgoing)
	go func() {
		defer networkTasks.Done()
		handleUDPConnection(serverCtx, server)
	}()
	go func() {
		networkTasks.Wait()
		close(server.done)
	}()

	return server, nil
}

func closeUDPConnOnContext(ctx context.Context, conn *net.UDPConn) {
	<-ctx.Done()
	err := conn.Close()
	if err != nil && !errors.Is(err, net.ErrClosed) {
		logCloseFailure(conn.LocalAddr().String(), err)
	}
}

func handleUDPConnection(ctx context.Context, server *UdpServer) {
	defer close(server.Incoming)
	for {
		packet, source, err := readUDPDatagram(server.conn)
		if err != nil {
			if isUDPServerClosed(ctx, err) {
				return
			} else {
				logUDPReadFailure(server.localAddress, err)
			}
			continue
		}
		if isSTUNPacket(packet) {
			err = server.handleSTUNPacket(ctx, packet, source)
			if err != nil {
				logSTUNPacketFailure(source, err)
			}
			continue
		}
		message, err := decodeUDPMessage(packet, net.UDPAddrFromAddrPort(source))
		if err != nil {
			logUDPReadFailure(server.localAddress, err)
			continue
		}
		server.stats.addReceived()
		select {
		case server.Incoming <- message:
		case <-ctx.Done():
			return
		}
	}
}

func isUDPServerClosed(ctx context.Context, err error) bool {
	if errors.Is(err, net.ErrClosed) {
		return true
	} else {
		select {
		case <-ctx.Done():
			return true
		default:
			return false
		}
	}
}

func readUDPMessage(conn *net.UDPConn) (UdpMessage, error) {
	packet, source, err := readUDPDatagram(conn)
	if err != nil {
		return UdpMessage{}, err
	}
	return decodeUDPMessage(packet, net.UDPAddrFromAddrPort(source))
}

func readUDPDatagram(conn *net.UDPConn) ([]byte, netip.AddrPort, error) {
	buffer := make([]byte, packetReadSize)
	n, source, err := conn.ReadFromUDPAddrPort(buffer)
	if err != nil {
		return nil, netip.AddrPort{}, fmt.Errorf("read UDP packet: %w", err)
	}
	return append([]byte(nil), buffer[:n]...), normalizeEndpoint(source), nil
}

func udpWriter(ctx context.Context, conn *net.UDPConn, outgoing chan UdpMessage, control chan outboundDatagram, results chan WriteResult, stats *DeliveryStats) {
	defer close(results)
	for {
		select {
		case <-ctx.Done():
			return
		case datagram := <-control:
			writeControlDatagram(ctx, conn, datagram)
		case message, ok := <-outgoing:
			if !ok {
				return
			}
			writeApplicationMessage(conn, message, results, stats)
		}
	}
}

func writeUDPMessage(conn *net.UDPConn, message UdpMessage) error {
	_, _, err := writeUDPMessageDetailed(conn, message)
	return err
}

func writeUDPMessageDetailed(conn *net.UDPConn, message UdpMessage) (netip.AddrPort, bool, error) {
	packet, err := encodeUDPMessage(message)
	if err != nil {
		return netip.AddrPort{}, true, err
	}

	address := endpointString(message.Address, message.Port)
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return netip.AddrPort{}, true, fmt.Errorf("resolve UDP target address %q: %w", address, err)
	}
	target := normalizeEndpoint(udpAddr.AddrPort())

	_, err = conn.WriteToUDPAddrPort(packet, target)
	if err != nil {
		return target, false, fmt.Errorf("write UDP packet to %q: %w", address, err)
	}

	return target, false, nil
}

func writeApplicationMessage(conn *net.UDPConn, message UdpMessage, results chan WriteResult, stats *DeliveryStats) {
	target, permanent, err := writeUDPMessageDetailed(conn, message)
	result := WriteResult{SessionID: message.SessionID, MessageID: message.MessageID, Sequence: message.Sequence, Target: target, Finished: time.Now(), Permanent: permanent}
	if err != nil {
		result.Error = err.Error()
		stats.addFailedWrite()
		logUDPWriteFailure(message, err)
	} else {
		stats.addSent()
	}
	if message.Type == ackType {
		return
	}
	select {
	case results <- result:
	default:
	}
}

func writeControlDatagram(ctx context.Context, conn *net.UDPConn, datagram outboundDatagram) {
	_, err := conn.WriteToUDPAddrPort(datagram.Packet, datagram.Destination)
	if err != nil {
		err = fmt.Errorf("%s to %v failed: %w", datagram.Operation, datagram.Destination, err)
		logControlWriteFailure(datagram.Operation, datagram.Destination, err)
	}
	select {
	case datagram.Result <- err:
	case <-ctx.Done():
	}
}

// StartUdpContext starts a local UDP server and returns immediately with a closeable server handle.
func StartUdpContext(ctx context.Context, hostName, portNum string, processor func(incoming, outgoing chan UdpMessage)) (*UdpServer, error) {
	config, err := ResolveRetryConfig(RetryConfig{})
	if err != nil {
		return nil, err
	}
	return startUdpContext(ctx, hostName, portNum, config.QueueLength, NewDeliveryStats(), nil, processor)
}

// StartUdp starts a local UDP server and blocks until the server stops.
func StartUdp(hostName, portNum string, processor func(incoming, outgoing chan UdpMessage)) (chan UdpMessage, chan UdpMessage) {
	server, err := StartUdpContext(context.Background(), hostName, portNum, processor)
	if err != nil {
		panic(err)
	}
	<-server.Done()
	return server.Incoming, server.Outgoing
}

// SendMessage copies data and queues it for delivery to server:port.
func SendMessage(outchan chan UdpMessage, data []byte, server string, port int) {
	payload := append([]byte(nil), data...)
	outchan <- UdpMessage{Data: payload, Address: server, Port: port, Sequence: getSequence(), Type: "", Cached: time.Now()}
}

// NewAcknowledgement returns an acknowledgement preserving the message's protocol identity and target.
func NewAcknowledgement(message UdpMessage) UdpMessage {
	return acknowledgementForMessage(message, time.Now())
}
