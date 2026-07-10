package brk

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
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

func startUdpContext(ctx context.Context, hostName, portNum string, queueLength int, stats *DeliveryStats, processor func(incoming, outgoing chan UdpMessage)) (*UdpServer, error) {
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
	udpAddr, err := net.ResolveUDPAddr("udp4", service)
	if err != nil {
		return nil, fmt.Errorf("resolve local UDP listen address %q: %w", service, err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("listen for UDP on %q: %w", service, err)
	}

	serverCtx, cancel := context.WithCancel(ctx)
	server := &UdpServer{
		Incoming:     make(chan UdpMessage, queueLength),
		Outgoing:     make(chan UdpMessage, queueLength),
		localAddress: conn.LocalAddr().String(),
		conn:         conn,
		stats:        stats,
		cancel:       cancel,
		done:         make(chan struct{}),
	}

	networkTasks := sync.WaitGroup{}
	networkTasks.Add(3)
	go func() {
		defer networkTasks.Done()
		closeUDPConnOnContext(serverCtx, conn)
	}()
	go func() {
		defer networkTasks.Done()
		udpWriter(serverCtx, conn, server.Outgoing, stats)
	}()
	go processor(server.Incoming, server.Outgoing)
	go func() {
		defer networkTasks.Done()
		handleUDPConnection(serverCtx, conn, server.Incoming, stats)
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

func handleUDPConnection(ctx context.Context, conn *net.UDPConn, incoming chan UdpMessage, stats *DeliveryStats) {
	defer close(incoming)
	for {
		message, err := readUDPMessage(conn)
		if err != nil {
			if isUDPServerClosed(ctx, err) {
				return
			} else {
				logUDPReadFailure(conn.LocalAddr().String(), err)
			}
		} else {
			stats.addReceived()
			select {
			case incoming <- message:
			case <-ctx.Done():
				return
			}
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
	buffer := make([]byte, packetReadSize)
	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("read UDP packet: %w", err)
	}
	return decodeUDPMessage(buffer[:n], addr)
}

func decodeUDPMessage(packet []byte, addr *net.UDPAddr) (UdpMessage, error) {
	if len(packet) > maxPacketSize {
		return UdpMessage{}, fmt.Errorf("decode UDP packet from %v: expected at most %d encoded bytes, received at least %d", addr, maxPacketSize, len(packet))
	}
	var message UdpMessage
	err := json.Unmarshal(packet, &message)
	if err != nil {
		return UdpMessage{}, fmt.Errorf("decode UDP packet from %v as brk.UdpMessage: %w", addr, err)
	}

	message.Address = addr.IP.String()
	message.Port = addr.Port
	return message, nil
}

func udpWriter(ctx context.Context, conn *net.UDPConn, outgoing chan UdpMessage, stats *DeliveryStats) {
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-outgoing:
			if !ok {
				return
			}
			err := writeUDPMessage(conn, message)
			if err != nil {
				stats.addFailedWrite()
				logUDPWriteFailure(message, err)
			} else {
				stats.addSent()
			}
		}
	}
}

func writeUDPMessage(conn *net.UDPConn, message UdpMessage) error {
	packet, err := encodeUDPMessage(message)
	if err != nil {
		return err
	}

	address := net.JoinHostPort(message.Address, strconv.Itoa(message.Port))
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return fmt.Errorf("resolve UDP target address %q: %w", address, err)
	}

	_, err = conn.WriteToUDP(packet, udpAddr)
	if err != nil {
		return fmt.Errorf("write UDP packet to %q: %w", address, err)
	}

	return nil
}

func encodeUDPMessage(message UdpMessage) ([]byte, error) {
	if message.Port < 1 || message.Port > 65535 {
		return nil, fmt.Errorf("expected UDP target port in 1..65535, received %d", message.Port)
	}

	packet, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode brk.UdpMessage sequence %d as JSON: %w", message.Sequence, err)
	}
	if len(packet) > maxPacketSize {
		return nil, fmt.Errorf("encode brk.UdpMessage sequence %d for UDP: expected at most %d encoded bytes, received %d from %d payload bytes", message.Sequence, maxPacketSize, len(packet), len(message.Data))
	}
	return packet, nil
}

// StartUdpContext starts a local UDP server and returns immediately with a closeable server handle.
func StartUdpContext(ctx context.Context, hostName, portNum string, processor func(incoming, outgoing chan UdpMessage)) (*UdpServer, error) {
	config, err := ResolveRetryConfig(RetryConfig{})
	if err != nil {
		return nil, err
	}
	return startUdpContext(ctx, hostName, portNum, config.QueueLength, NewDeliveryStats(), processor)
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
