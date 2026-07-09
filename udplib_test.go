package brk

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestResolveRetryConfigUsesDefaults(t *testing.T) {
	config, err := ResolveRetryConfig(RetryConfig{})
	if err != nil {
		t.Fatalf("resolve retry config failed: expected nil error, received %v", err)
	}

	if config.QueueLength != Qlength {
		t.Fatalf("queue length mismatch: expected %d, received %d", Qlength, config.QueueLength)
	}
	if config.RetryInterval != 5*time.Second {
		t.Fatalf("retry interval mismatch: expected %v, received %v", 5*time.Second, config.RetryInterval)
	}
	if config.AckTimeout != 2*time.Second {
		t.Fatalf("ack timeout mismatch: expected %v, received %v", 2*time.Second, config.AckTimeout)
	}
	if config.MaxAttempts != 0 {
		t.Fatalf("max attempts mismatch: expected %d, received %d", 0, config.MaxAttempts)
	}
	if config.DuplicateTTL != 5*time.Minute {
		t.Fatalf("duplicate TTL mismatch: expected %v, received %v", 5*time.Minute, config.DuplicateTTL)
	}
}

func TestResolveRetryConfigRejectsInvalidValues(t *testing.T) {
	_, err := ResolveRetryConfig(RetryConfig{QueueLength: -1})
	if err == nil {
		t.Fatalf("negative queue length mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{RetryInterval: -1})
	if err == nil {
		t.Fatalf("negative retry interval mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{AckTimeout: -1})
	if err == nil {
		t.Fatalf("negative ack timeout mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{MaxAttempts: -1})
	if err == nil {
		t.Fatalf("negative max attempts mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{DuplicateTTL: -1})
	if err == nil {
		t.Fatalf("negative duplicate TTL mismatch: expected error, received nil")
	}
}

func TestSendMessageQueuesMessage(t *testing.T) {
	outgoing := make(chan UdpMessage, 1)
	before := time.Now()

	SendMessage(outgoing, []byte("ping"), "127.0.0.1", 9000)

	message := <-outgoing
	if !bytes.Equal(message.Data, []byte("ping")) {
		t.Fatalf("queued data mismatch: expected %q, received %q", "ping", string(message.Data))
	}
	if message.Address != "127.0.0.1" {
		t.Fatalf("queued address mismatch: expected %q, received %q", "127.0.0.1", message.Address)
	}
	if message.Port != 9000 {
		t.Fatalf("queued port mismatch: expected %d, received %d", 9000, message.Port)
	}
	if message.Sequence <= 0 {
		t.Fatalf("queued sequence mismatch: expected positive sequence, received %d", message.Sequence)
	}
	if message.Cached.Before(before) {
		t.Fatalf("queued cache time mismatch: expected at or after %v, received %v", before, message.Cached)
	}
}

func TestWriteAndReadUDPMessage(t *testing.T) {
	server := listenLocalUDP(t)
	defer closeUDPConn(t, server)
	sender := listenLocalUDP(t)
	defer closeUDPConn(t, sender)

	serverAddr := server.LocalAddr().(*net.UDPAddr)
	senderAddr := sender.LocalAddr().(*net.UDPAddr)
	sent := UdpMessage{Data: []byte("payload"), Address: "127.0.0.1", Port: serverAddr.Port, Sequence: 42, Type: "", Cached: time.Now()}

	err := writeUDPMessage(sender, sent)
	if err != nil {
		t.Fatalf("write UDP message failed: expected nil error, received %v", err)
	}

	err = server.SetReadDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("set UDP read deadline failed: expected nil error, received %v", err)
	}
	received, err := readUDPMessage(server)
	if err != nil {
		t.Fatalf("read UDP message failed: expected nil error, received %v", err)
	}

	if !bytes.Equal(received.Data, sent.Data) {
		t.Fatalf("received data mismatch: expected %q, received %q", string(sent.Data), string(received.Data))
	}
	if received.Sequence != sent.Sequence {
		t.Fatalf("received sequence mismatch: expected %d, received %d", sent.Sequence, received.Sequence)
	}
	if received.Address != "127.0.0.1" {
		t.Fatalf("received source address mismatch: expected %q, received %q", "127.0.0.1", received.Address)
	}
	if received.Port != senderAddr.Port {
		t.Fatalf("received source port mismatch: expected %d, received %d", senderAddr.Port, received.Port)
	}
}

func TestWriteUDPMessageRejectsInvalidPort(t *testing.T) {
	sender := listenLocalUDP(t)
	defer closeUDPConn(t, sender)

	err := writeUDPMessage(sender, UdpMessage{Address: "127.0.0.1", Port: 0, Sequence: 7})
	if err == nil {
		t.Fatalf("invalid port write mismatch: expected error, received nil")
	}
}

func TestStartUdpContextReceivesAndCloses(t *testing.T) {
	received := make(chan UdpMessage, 1)
	server, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		received <- <-incoming
	})
	if err != nil {
		t.Fatalf("start UDP context failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, server)

	host, port := splitHostPort(t, server.LocalAddress())
	sender := listenLocalUDP(t)
	defer closeUDPConn(t, sender)

	err = writeUDPMessage(sender, UdpMessage{Data: []byte("hello"), Address: host, Port: port, Sequence: 99})
	if err != nil {
		t.Fatalf("write UDP message failed: expected nil error, received %v", err)
	}

	message := receiveMessage(t, received)
	if string(message.Data) != "hello" {
		t.Fatalf("server delivery mismatch: expected %q, received %q", "hello", string(message.Data))
	}
	if server.Stats().Received != 1 {
		t.Fatalf("server received stats mismatch: expected %d, received %d", 1, server.Stats().Received)
	}
}

func TestHandleRetryIncomingAcknowledgesNewMessagesAndDropsDuplicates(t *testing.T) {
	seenCache := map[int]time.Time{}
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	config := DefaultRetryConfig()
	stats := NewDeliveryStats()
	appincoming := make(chan UdpMessage, 1)
	netoutgoing := make(chan UdpMessage, 2)
	message := UdpMessage{Data: []byte("hello"), Address: "127.0.0.1", Port: 9876, Sequence: 88}

	handleRetryIncoming(message, seenCache, cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())

	ack := <-netoutgoing
	if ack.Type != ackType {
		t.Fatalf("ack type mismatch: expected %q, received %q", ackType, ack.Type)
	}
	if ack.Sequence != message.Sequence {
		t.Fatalf("ack sequence mismatch: expected %d, received %d", message.Sequence, ack.Sequence)
	}
	delivered := <-appincoming
	if delivered.Sequence != message.Sequence {
		t.Fatalf("delivered sequence mismatch: expected %d, received %d", message.Sequence, delivered.Sequence)
	}

	handleRetryIncoming(message, seenCache, cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())
	duplicateAck := <-netoutgoing
	if duplicateAck.Type != ackType {
		t.Fatalf("duplicate ack type mismatch: expected %q, received %q", ackType, duplicateAck.Type)
	}

	select {
	case duplicate := <-appincoming:
		t.Fatalf("duplicate delivery mismatch: expected no app message, received sequence %d", duplicate.Sequence)
	default:
	}

	if stats.Snapshot().Duplicates != 1 {
		t.Fatalf("duplicate stats mismatch: expected %d, received %d", 1, stats.Snapshot().Duplicates)
	}
}

func TestHandleRetryIncomingRemovesAcknowledgedCacheEntry(t *testing.T) {
	seenCache := map[int]time.Time{}
	cache := map[int]retryCacheEntry{55: {Message: UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55}}}
	cacheLock := sync.Mutex{}
	config := DefaultRetryConfig()
	stats := NewDeliveryStats()
	appincoming := make(chan UdpMessage, 1)
	netoutgoing := make(chan UdpMessage, 1)

	handleRetryIncoming(UdpMessage{Sequence: 55, Type: ackType}, seenCache, cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())

	cacheLock.Lock()
	_, exists := cache[55]
	cacheLock.Unlock()
	if exists {
		t.Fatalf("cache removal mismatch: expected sequence %d to be absent", 55)
	}
	if stats.Snapshot().Acked != 1 {
		t.Fatalf("ack stats mismatch: expected %d, received %d", 1, stats.Snapshot().Acked)
	}
}

func TestRememberIncomingSequenceExpiresOldEntries(t *testing.T) {
	now := time.Now()
	seenCache := map[int]time.Time{10: now.Add(-2 * time.Second)}

	newMessage := rememberIncomingSequence(10, seenCache, time.Second, now)
	if !newMessage {
		t.Fatalf("expired duplicate cache mismatch: expected sequence to be accepted after TTL")
	}
}

func TestRetransmitExpiredMessages(t *testing.T) {
	now := time.Now()
	cache := map[int]retryCacheEntry{
		10: {Message: UdpMessage{Address: "127.0.0.1", Port: 1200, Sequence: 10, Cached: now.Add(-3 * time.Second)}},
		11: {Message: UdpMessage{Address: "127.0.0.1", Port: 1201, Sequence: 11, Cached: now}},
	}
	cacheLock := sync.Mutex{}
	netoutgoing := make(chan UdpMessage, 1)
	stats := NewDeliveryStats()
	config := DefaultRetryConfig()

	count := retransmitExpiredMessages(cache, &cacheLock, netoutgoing, now, config, stats)
	if count != 1 {
		t.Fatalf("retransmit count mismatch: expected %d, received %d", 1, count)
	}

	retried := <-netoutgoing
	if retried.Sequence != 10 {
		t.Fatalf("retransmitted sequence mismatch: expected %d, received %d", 10, retried.Sequence)
	}

	cacheLock.Lock()
	cached := cache[10]
	cacheLock.Unlock()
	if !cached.Message.Cached.Equal(now) {
		t.Fatalf("cache timestamp mismatch: expected %v, received %v", now, cached.Message.Cached)
	}
	if cached.Attempts != 1 {
		t.Fatalf("retry attempts mismatch: expected %d, received %d", 1, cached.Attempts)
	}
	if stats.Snapshot().Retried != 1 {
		t.Fatalf("retry stats mismatch: expected %d, received %d", 1, stats.Snapshot().Retried)
	}

	select {
	case unexpected := <-netoutgoing:
		t.Fatalf("fresh retransmission mismatch: expected no second message, received sequence %d", unexpected.Sequence)
	default:
	}
}

func TestRetransmitExpiredMessagesDropsAfterMaxAttempts(t *testing.T) {
	now := time.Now()
	cache := map[int]retryCacheEntry{
		10: {Message: UdpMessage{Address: "127.0.0.1", Port: 1200, Sequence: 10, Cached: now.Add(-3 * time.Second)}, Attempts: 1},
	}
	cacheLock := sync.Mutex{}
	netoutgoing := make(chan UdpMessage, 1)
	stats := NewDeliveryStats()
	config := DefaultRetryConfig()
	config.MaxAttempts = 1

	count := retransmitExpiredMessages(cache, &cacheLock, netoutgoing, now, config, stats)
	if count != 0 {
		t.Fatalf("dropped retransmit count mismatch: expected %d, received %d", 0, count)
	}

	cacheLock.Lock()
	_, exists := cache[10]
	cacheLock.Unlock()
	if exists {
		t.Fatalf("dropped cache mismatch: expected sequence %d to be absent", 10)
	}
	if stats.Snapshot().Dropped != 1 {
		t.Fatalf("dropped stats mismatch: expected %d, received %d", 1, stats.Snapshot().Dropped)
	}
}

func TestStartRetryUdpContextRetriesUntilAcked(t *testing.T) {
	seenByReceiver := make(chan UdpMessage, 2)
	receiver, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		count := 0
		for message := range incoming {
			count = count + 1
			seenByReceiver <- message
			if count >= 2 {
				outgoing <- UdpMessage{Address: message.Address, Port: message.Port, Sequence: message.Sequence, Type: ackType, Cached: time.Now()}
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("start raw receiver failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, receiver)

	config := RetryConfig{
		QueueLength:   10,
		RetryInterval: 20 * time.Millisecond,
		AckTimeout:    10 * time.Millisecond,
		MaxAttempts:   3,
		DuplicateTTL:  time.Second,
	}
	sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", config, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start retry sender failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, sender)

	host, port := splitHostPort(t, receiver.LocalAddress())
	sender.Outgoing <- UdpMessage{Data: []byte("retry me"), Address: host, Port: port}

	first := receiveMessage(t, seenByReceiver)
	second := receiveMessage(t, seenByReceiver)
	if first.Sequence != second.Sequence {
		t.Fatalf("retry sequence mismatch: expected %d, received %d", first.Sequence, second.Sequence)
	}
	waitForStats(t, sender, func(snapshot DeliveryStatsSnapshot) bool {
		return snapshot.Retried >= 1 && snapshot.Acked >= 1
	})
}

func listenLocalUDP(t *testing.T) *net.UDPConn {
	t.Helper()
	addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen local UDP failed: expected nil error, received %v", err)
	}
	return conn
}

func closeUDPConn(t *testing.T, conn *net.UDPConn) {
	t.Helper()
	err := conn.Close()
	if err != nil {
		t.Fatalf("close UDP connection failed: expected nil error, received %v", err)
	}
}

func closeUdpServer(t *testing.T, server *UdpServer) {
	t.Helper()
	err := server.Close()
	if err != nil {
		t.Fatalf("close UDP server failed: expected nil error, received %v", err)
	}
}

func closeRetryServer(t *testing.T, server *RetryUdpServer) {
	t.Helper()
	err := server.Close()
	if err != nil {
		t.Fatalf("close retry UDP server failed: expected nil error, received %v", err)
	}
}

func splitHostPort(t *testing.T, address string) (string, int) {
	t.Helper()
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split host port failed: expected nil error for %q, received %v", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port failed: expected integer for %q, received %v", portText, err)
	}
	return host, port
}

func receiveMessage(t *testing.T, messages chan UdpMessage) UdpMessage {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(2 * time.Second):
		t.Fatalf("receive message timed out: expected UDP message within %v", 2*time.Second)
		return UdpMessage{}
	}
}

func waitForStats(t *testing.T, server *RetryUdpServer, ready func(snapshot DeliveryStatsSnapshot) bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ready(server.Stats()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stats wait timed out: last snapshot=%+v", server.Stats())
}
