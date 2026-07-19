package brk

import (
	"bytes"
	"context"
	"fmt"
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
	if config.MaxPending != Qlength {
		t.Fatalf("max pending mismatch: expected %d, received %d", Qlength, config.MaxPending)
	}
	if config.BackoffMultiplier != defaultBackoffMultiplier {
		t.Fatalf("backoff multiplier mismatch: expected %v, received %v", defaultBackoffMultiplier, config.BackoffMultiplier)
	}
	if config.MaxRetryDelay != 30*time.Second {
		t.Fatalf("max retry delay mismatch: expected %v, received %v", 30*time.Second, config.MaxRetryDelay)
	}
	if config.JitterFraction != defaultJitterFraction {
		t.Fatalf("jitter fraction mismatch: expected %v, received %v", defaultJitterFraction, config.JitterFraction)
	}
	if config.DeliveryTimeout != time.Minute {
		t.Fatalf("delivery timeout mismatch: expected %v, received %v", time.Minute, config.DeliveryTimeout)
	}
	if len(config.AuthenticationKey) != 0 {
		t.Fatalf("authentication key mismatch: expected empty key, received %d bytes", len(config.AuthenticationKey))
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

	_, err = ResolveRetryConfig(RetryConfig{MaxPending: -1})
	if err == nil {
		t.Fatalf("negative max pending mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{BackoffMultiplier: 0.5})
	if err == nil {
		t.Fatalf("small backoff multiplier mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{MaxRetryDelay: -1})
	if err == nil {
		t.Fatalf("negative max retry delay mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{JitterFraction: 1})
	if err == nil {
		t.Fatalf("large jitter fraction mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{DeliveryTimeout: -1})
	if err == nil {
		t.Fatalf("negative delivery timeout mismatch: expected error, received nil")
	}

	_, err = ResolveRetryConfig(RetryConfig{AuthenticationKey: []byte("short")})
	if err == nil {
		t.Fatalf("short authentication key mismatch: expected error, received nil")
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

func TestSendMessageCopiesData(t *testing.T) {
	outgoing := make(chan UdpMessage, 1)
	payload := []byte("ping")

	SendMessage(outgoing, payload, "127.0.0.1", 9000)
	payload[0] = 'x'

	message := receiveMessage(t, outgoing)
	if string(message.Data) != "ping" {
		t.Fatalf("queued data ownership mismatch: expected %q after caller mutation, received %q", "ping", string(message.Data))
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

func TestWriteAndReadMultiKilobyteUDPMessage(t *testing.T) {
	server := listenLocalUDP(t)
	defer closeUDPConn(t, server)
	sender := listenLocalUDP(t)
	defer closeUDPConn(t, sender)

	serverAddr := server.LocalAddr().(*net.UDPAddr)
	payload := bytes.Repeat([]byte("x"), 4*1024)
	sent := UdpMessage{Data: payload, Address: "127.0.0.1", Port: serverAddr.Port, Sequence: 43, Cached: time.Now()}

	err := writeUDPMessage(sender, sent)
	if err != nil {
		t.Fatalf("write large UDP message failed: expected nil error, received %v", err)
	}
	err = server.SetReadDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("set large UDP read deadline failed: expected nil error, received %v", err)
	}
	received, err := readUDPMessage(server)
	if err != nil {
		t.Fatalf("read large UDP message failed: expected nil error, received %v", err)
	}
	if !bytes.Equal(received.Data, payload) {
		t.Fatalf("large UDP payload mismatch: expected %d bytes, received %d", len(payload), len(received.Data))
	}
}

func TestWriteUDPMessageRejectsOversizedPacket(t *testing.T) {
	sender := listenLocalUDP(t)
	defer closeUDPConn(t, sender)

	err := writeUDPMessage(sender, UdpMessage{Data: bytes.Repeat([]byte("x"), maxPacketSize), Address: "127.0.0.1", Port: 9000, Sequence: 44})
	if err == nil {
		t.Fatalf("oversized UDP packet mismatch: expected error, received nil")
	}
}

func TestReadUDPMessageRejectsOversizedPacket(t *testing.T) {
	_, err := decodeUDPMessage(bytes.Repeat([]byte("x"), packetReadSize), &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 9000})
	if err == nil {
		t.Fatalf("oversized UDP read mismatch: expected error, received nil")
	}
	expected := fmt.Sprintf("decode UDP packet from 127.0.0.1:9000: expected at most %d encoded bytes, received at least %d", maxPacketSize, packetReadSize)
	if err.Error() != expected {
		t.Fatalf("oversized UDP read error mismatch: expected %q, received %q", expected, err.Error())
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

func TestStartUdpContextRejectsNilProcessor(t *testing.T) {
	server, err := StartUdpContext(context.Background(), "127.0.0.1", "0", nil)
	if err == nil {
		t.Fatalf("nil UDP processor mismatch: expected error, received nil")
	}
	if server != nil {
		t.Fatalf("nil UDP processor server mismatch: expected nil server, received %v", server)
	}
}

func TestStartRetryUdpContextRejectsNilProcessor(t *testing.T) {
	server, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{}, nil)
	if err == nil {
		t.Fatalf("nil retry processor mismatch: expected error, received nil")
	}
	if server != nil {
		t.Fatalf("nil retry processor server mismatch: expected nil server, received %v", server)
	}
}

func TestUdpServerCloseClosesIncoming(t *testing.T) {
	processorDone := make(chan struct{})
	server, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
		close(processorDone)
	})
	if err != nil {
		t.Fatalf("start UDP context failed: expected nil error, received %v", err)
	}

	err = server.Close()
	if err != nil {
		t.Fatalf("close UDP context failed: expected nil error, received %v", err)
	}
	select {
	case <-processorDone:
	case <-time.After(time.Second):
		t.Fatalf("UDP processor shutdown timed out: expected Incoming to close within %v", time.Second)
	}
}

func TestRetryUdpServerCloseClosesIncoming(t *testing.T) {
	processorDone := make(chan struct{})
	server, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{}, func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
		close(processorDone)
	})
	if err != nil {
		t.Fatalf("start retry UDP context failed: expected nil error, received %v", err)
	}

	err = server.Close()
	if err != nil {
		t.Fatalf("close retry UDP context failed: expected nil error, received %v", err)
	}
	select {
	case <-processorDone:
	case <-time.After(time.Second):
		t.Fatalf("retry UDP processor shutdown timed out: expected Incoming to close within %v", time.Second)
	}
	select {
	case <-server.Done():
	default:
		t.Fatalf("retry UDP done mismatch: expected Done to be closed after Close returns")
	}
}

func TestRetryUdpServerStopsWhenNetworkCloses(t *testing.T) {
	server, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{}, func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
	})
	if err != nil {
		t.Fatalf("start retry UDP context failed: expected nil error, received %v", err)
	}

	err = server.Network.Close()
	if err != nil {
		t.Fatalf("close retry network failed: expected nil error, received %v", err)
	}
	select {
	case <-server.Done():
	case <-time.After(time.Second):
		t.Fatalf("retry UDP network shutdown timed out: expected Done within %v", time.Second)
	}
}

func TestHandleRetryIncomingAcknowledgesNewMessagesAndDropsDuplicates(t *testing.T) {
	seenCache := newReceivedMessageCache()
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	config := DefaultRetryConfig()
	stats := NewDeliveryStats()
	appincoming := make(chan UdpMessage, 1)
	netoutgoing := make(chan UdpMessage, 2)
	message := UdpMessage{Data: []byte("hello"), Address: "127.0.0.1", Port: 9876, Sequence: 88}

	handleRetryIncoming(context.Background(), message, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())

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

	handleRetryIncoming(context.Background(), message, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())
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

func TestHandleRetryIncomingKeepsSameSequenceFromDifferentPeers(t *testing.T) {
	seenCache := newReceivedMessageCache()
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	config := DefaultRetryConfig()
	stats := NewDeliveryStats()
	appincoming := make(chan UdpMessage, 2)
	netoutgoing := make(chan UdpMessage, 2)
	first := UdpMessage{Data: []byte("first"), Address: "127.0.0.1", Port: 1001, Sequence: 88}
	second := UdpMessage{Data: []byte("second"), Address: "127.0.0.1", Port: 1002, Sequence: 88}

	handleRetryIncoming(context.Background(), first, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())
	handleRetryIncoming(context.Background(), second, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())

	if received := receiveMessage(t, appincoming); string(received.Data) != "first" {
		t.Fatalf("first peer delivery mismatch: expected %q, received %q", "first", string(received.Data))
	}
	if received := receiveMessage(t, appincoming); string(received.Data) != "second" {
		t.Fatalf("second peer delivery mismatch: expected %q, received %q", "second", string(received.Data))
	}
	if stats.Snapshot().Duplicates != 0 {
		t.Fatalf("cross-peer duplicate stats mismatch: expected %d, received %d", 0, stats.Snapshot().Duplicates)
	}
	if len(netoutgoing) != 2 {
		t.Fatalf("cross-peer acknowledgement count mismatch: expected %d, received %d", 2, len(netoutgoing))
	}
}

func TestHandleRetryIncomingDoesNotAcknowledgeUndeliveredMessage(t *testing.T) {
	seenCache := newReceivedMessageCache()
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	appincoming := make(chan UdpMessage)
	netoutgoing := make(chan UdpMessage, 1)
	message := UdpMessage{Data: []byte("hello"), Address: "127.0.0.1", Port: 9876, Sequence: 89}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handled := handleRetryIncoming(ctx, message, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, DefaultRetryConfig(), appincoming, netoutgoing, NewDeliveryStats(), time.Now())
	if handled {
		t.Fatalf("cancelled retry handler mismatch: expected false, received true")
	}
	if len(netoutgoing) != 0 {
		t.Fatalf("undelivered acknowledgement count mismatch: expected %d, received %d", 0, len(netoutgoing))
	}
}

func TestHandleRetryIncomingRemovesAcknowledgedCacheEntry(t *testing.T) {
	seenCache := newReceivedMessageCache()
	cache := map[int]retryCacheEntry{55: {Message: UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55}}}
	cacheLock := sync.Mutex{}
	config := DefaultRetryConfig()
	stats := NewDeliveryStats()
	appincoming := make(chan UdpMessage, 1)
	netoutgoing := make(chan UdpMessage, 1)

	handleRetryIncoming(context.Background(), UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55, Type: ackType}, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())

	cacheLock.Lock()
	_, exists := cache[55]
	cacheLock.Unlock()
	if exists {
		t.Fatalf("cache removal mismatch: expected sequence %d to be absent", 55)
	}
	if stats.Snapshot().Acked != 1 {
		t.Fatalf("ack stats mismatch: expected %d, received %d", 1, stats.Snapshot().Acked)
	}

	handleRetryIncoming(context.Background(), UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55, Type: ackType}, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, config, appincoming, netoutgoing, stats, time.Now())
	if stats.Snapshot().Acked != 1 {
		t.Fatalf("unknown ack stats mismatch: expected %d, received %d", 1, stats.Snapshot().Acked)
	}
}

func TestHandleRetryIncomingRejectsWrongPeerAcknowledgement(t *testing.T) {
	seenCache := newReceivedMessageCache()
	cache := map[int]retryCacheEntry{55: {Message: UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55}}}
	cacheLock := sync.Mutex{}
	stats := NewDeliveryStats()

	wrongAcknowledgements := []UdpMessage{
		{Address: "127.0.0.1", Port: 4321, Sequence: 55, Type: ackType},
		{Address: "127.0.0.2", Port: 1234, Sequence: 55, Type: ackType},
	}
	for _, acknowledgement := range wrongAcknowledgements {
		handleRetryIncoming(context.Background(), acknowledgement, seenCache, newReassemblyCache(), newOrderingCache(), cache, &cacheLock, DefaultRetryConfig(), make(chan UdpMessage, 1), make(chan UdpMessage, 1), stats, time.Now())
	}

	cacheLock.Lock()
	_, exists := cache[55]
	cacheLock.Unlock()
	if !exists {
		t.Fatalf("wrong-peer acknowledgement mismatch: expected sequence %d to remain cached", 55)
	}
	if stats.Snapshot().Acked != 0 {
		t.Fatalf("wrong-peer acknowledgement stats mismatch: expected %d, received %d", 0, stats.Snapshot().Acked)
	}
}

func TestAcknowledgementDoesNotRestorePreparedRetry(t *testing.T) {
	now := time.Now()
	cache := map[int]retryCacheEntry{
		55: {Message: UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55, Cached: now.Add(-3 * time.Second)}, Attempts: 1},
	}
	cacheLock := sync.Mutex{}
	action := prepareRetryAction(cache, &cacheLock, 55, now, DefaultRetryConfig())
	if !action.Send || action.Terminal {
		t.Fatalf("prepared retry mismatch: expected send=true and terminal=false, received send=%v terminal=%v", action.Send, action.Terminal)
	}

	_, removed := removeAcknowledgedMessage(UdpMessage{Address: "127.0.0.1", Port: 1234, Sequence: 55, Type: ackType}, cache, &cacheLock)
	if !removed {
		t.Fatalf("prepared retry acknowledgement mismatch: expected cached sequence %d to be removed", 55)
	}
	netoutgoing := make(chan UdpMessage, 1)
	netoutgoing <- action.Entry.Message

	cacheLock.Lock()
	_, exists := cache[55]
	cacheLock.Unlock()
	if exists {
		t.Fatalf("prepared retry cache mismatch: expected sequence %d to remain absent after send", 55)
	}
}

func TestRememberIncomingMessageExpiresOldEntries(t *testing.T) {
	now := time.Now()
	message := UdpMessage{Address: "127.0.0.1", Port: 1000, Sequence: 10}
	seenCache := newReceivedMessageCache()

	if !rememberIncomingMessage(message, seenCache, time.Second, now.Add(-2*time.Second)) {
		t.Fatalf("first message mismatch: expected new identity to be accepted")
	}
	if rememberIncomingMessage(message, seenCache, time.Second, now.Add(-1500*time.Millisecond)) {
		t.Fatalf("live duplicate mismatch: expected repeat inside TTL to be suppressed")
	}

	newMessage := rememberIncomingMessage(message, seenCache, time.Second, now)
	if !newMessage {
		t.Fatalf("expired duplicate cache mismatch: expected sequence to be accepted after TTL")
	}
	if len(seenCache.entries) != 1 || len(seenCache.queue) != 1 {
		t.Fatalf("pruned cache size mismatch: expected 1 entry and 1 queued record, received %d and %d", len(seenCache.entries), len(seenCache.queue))
	}
}

func TestReceivedMessageCachePruneKeepsReaddedIdentity(t *testing.T) {
	now := time.Now()
	key := receivedMessageKey{Address: "127.0.0.1", Port: 1000, Sequence: 10}
	seenCache := newReceivedMessageCache()

	if !seenCache.remember(key, time.Second, now.Add(-3*time.Second)) {
		t.Fatalf("first remember mismatch: expected new identity to be accepted")
	}
	if !seenCache.remember(key, time.Second, now.Add(-500*time.Millisecond)) {
		t.Fatalf("re-added identity mismatch: expected identity to be accepted after its record expired")
	}

	seenCache.prune(time.Second, now)
	if _, exists := seenCache.entries[key]; !exists {
		t.Fatalf("prune mismatch: expected live re-added identity to survive pruning of its expired record")
	}
}

func TestRetransmitExpiredMessages(t *testing.T) {
	now := time.Now()
	cache := map[int]retryCacheEntry{
		10: {Message: UdpMessage{Address: "127.0.0.1", Port: 1200, Sequence: 10, Cached: now.Add(-3 * time.Second)}, Attempts: 1},
		11: {Message: UdpMessage{Address: "127.0.0.1", Port: 1201, Sequence: 11, Cached: now}, Attempts: 1},
	}
	cacheLock := sync.Mutex{}
	netoutgoing := make(chan UdpMessage, 1)
	stats := NewDeliveryStats()
	config := DefaultRetryConfig()

	count := retransmitExpiredMessages(context.Background(), cache, &cacheLock, netoutgoing, now, config, stats)
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
	if cached.Attempts != 2 {
		t.Fatalf("retry attempts mismatch: expected %d, received %d", 2, cached.Attempts)
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
		10: {Message: UdpMessage{Address: "127.0.0.1", Port: 1200, Sequence: 10, Cached: now.Add(-3 * time.Second)}, Attempts: 2},
	}
	cacheLock := sync.Mutex{}
	netoutgoing := make(chan UdpMessage, 1)
	stats := NewDeliveryStats()
	config := DefaultRetryConfig()
	config.MaxAttempts = 1

	count := retransmitExpiredMessages(context.Background(), cache, &cacheLock, netoutgoing, now, config, stats)
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

func TestQueueOutgoingRequestRejectsInvalidMessageWithoutCaching(t *testing.T) {
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	netoutgoing := make(chan UdpMessage, 1)
	stats := NewDeliveryStats()
	message := UdpMessage{Data: []byte("x"), Address: "", Port: 9000}

	config, err := ResolveRetryConfig(RetryConfig{})
	if err != nil {
		t.Fatalf("resolve retry config failed: expected nil error, received %v", err)
	}
	sessionID, err := newSessionID()
	if err != nil {
		t.Fatalf("create session id failed: expected nil error, received %v", err)
	}
	handled := queueOutgoingRequest(context.Background(), config, sessionID, outboundRequest{Message: message}, cache, &cacheLock, netoutgoing, stats)
	if !handled {
		t.Fatalf("invalid retry message handling mismatch: expected true, received false")
	}
	if len(cache) != 0 {
		t.Fatalf("invalid retry cache mismatch: expected %d entries, received %d", 0, len(cache))
	}
	if len(netoutgoing) != 0 {
		t.Fatalf("invalid network queue mismatch: expected %d messages, received %d", 0, len(netoutgoing))
	}
	if stats.Snapshot().Rejected != 1 {
		t.Fatalf("invalid retry rejection stats mismatch: expected %d, received %d", 1, stats.Snapshot().Rejected)
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
				outgoing <- NewAcknowledgement(message)
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
		MaxAttempts:   10,
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

func ExampleSendMessage() {
	outgoing := make(chan UdpMessage, 1)
	SendMessage(outgoing, []byte("ping"), "127.0.0.1", 9000)
	message := <-outgoing
	fmt.Printf("%s -> %s:%d\n", message.Data, message.Address, message.Port)
	// Output:
	// ping -> 127.0.0.1:9000
}
