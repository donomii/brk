package brk

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

func orderedTestMessage(session SessionID, sequence int) UdpMessage {
	return UdpMessage{Data: []byte(strconv.Itoa(sequence)), Address: "10.0.0.1", Port: 5000, SessionID: session, Sequence: sequence}
}

func expectReleasedSequences(t *testing.T, released []UdpMessage, expected []int) {
	t.Helper()
	if len(released) != len(expected) {
		t.Fatalf("released count mismatch: expected %d messages %v, received %d", len(expected), expected, len(released))
	}
	for index, message := range released {
		if message.Sequence != expected[index] {
			t.Fatalf("released order mismatch at %d: expected sequence %d, received %d", index, expected[index], message.Sequence)
		}
	}
}

func TestOrderingCacheDeliversContiguousSequencesImmediately(t *testing.T) {
	cache := newOrderingCache()
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	for _, sequence := range []int{10, 11, 12} {
		expectReleasedSequences(t, cache.add(orderedTestMessage(session, sequence), sequence, now), []int{sequence})
	}
}

func TestOrderingCacheHoldsGapAndReleasesInOrder(t *testing.T) {
	cache := newOrderingCache()
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 10), 10, now), []int{10})
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 12), 12, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 13), 13, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 11), 11, now), []int{11, 12, 13})
}

func TestOrderingCacheReleasesHeldMessagesAfterGapTimeout(t *testing.T) {
	cache := newOrderingCache()
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 10), 10, now), []int{10})
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 12), 12, now), nil)
	expectReleasedSequences(t, cache.flush(time.Second, time.Minute, now.Add(500*time.Millisecond)), nil)
	expectReleasedSequences(t, cache.flush(time.Second, time.Minute, now.Add(time.Second)), []int{12})
}

func TestOrderingCacheDeliversLateArrivalBelowFloorImmediately(t *testing.T) {
	cache := newOrderingCache()
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 10), 10, now), []int{10})
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 12), 12, now), nil)
	expectReleasedSequences(t, cache.flush(time.Second, time.Minute, now.Add(time.Second)), []int{12})
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 11), 11, now.Add(2*time.Second)), []int{11})
}

func TestOrderingCacheKeepsSessionsIndependent(t *testing.T) {
	cache := newOrderingCache()
	sessionA := SessionID("00112233445566778899aabbccddeeff")
	sessionB := SessionID("ffeeddccbbaa99887766554433221100")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessionA, 10), 10, now), []int{10})
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessionA, 12), 12, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessionB, 12), 12, now), []int{12})
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessionA, 11), 11, now), []int{11, 12})
}

func TestOrderingCacheForgetsIdleStreams(t *testing.T) {
	cache := newOrderingCache()
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 10), 10, now), []int{10})
	expectReleasedSequences(t, cache.flush(time.Second, time.Minute, now.Add(2*time.Minute)), nil)
	if len(cache.streams) != 0 {
		t.Fatalf("idle stream prune mismatch: expected no remembered streams, received %d", len(cache.streams))
	}
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 5), 5, now.Add(2*time.Minute)), []int{5})
}

func TestOrderingCacheTreatsAssembledFragmentSpanAsContiguous(t *testing.T) {
	cache := newOrderingCache()
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 9), 9, now), []int{9})
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 13), 13, now), nil)
	assembled := orderedTestMessage(session, 12)
	expectReleasedSequences(t, cache.add(assembled, 10, now), []int{12, 13})
}

func TestOrderingCacheReleasesLowestSequenceAtPeerLimit(t *testing.T) {
	stats := NewDeliveryStats()
	cache := newOrderingCacheWithLimits(2, 10, stats)
	session := SessionID("00112233445566778899aabbccddeeff")
	now := time.Now()
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 10), 10, now), []int{10})
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 12), 12, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 14), 14, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(session, 16), 16, now), []int{12})

	stream := cache.streams[orderingStreamKey{Address: "10.0.0.1", Port: 5000, SessionID: session}]
	if cache.held != 2 || len(stream.held) != 2 {
		t.Fatalf("per-peer ordering limit mismatch: expected two held messages, received cache=%d peer=%d", cache.held, len(stream.held))
	}
	if snapshot := stats.Snapshot(); snapshot.OrderingCapacityReleases != 1 {
		t.Fatalf("per-peer ordering stats mismatch: expected one capacity release, received %d", snapshot.OrderingCapacityReleases)
	}
}

func TestOrderingCacheReleasesOldestStreamHeadAtGlobalLimit(t *testing.T) {
	stats := NewDeliveryStats()
	cache := newOrderingCacheWithLimits(4, 2, stats)
	now := time.Now()
	sessions := []SessionID{"00112233445566778899aabbccddeeff", "11112222333344445555666677778888", "9999aaaabbbbccccddddeeeeffff0000"}
	for _, session := range sessions {
		expectReleasedSequences(t, cache.add(orderedTestMessage(session, 10), 10, now), []int{10})
	}
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessions[0], 12), 12, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessions[1], 12), 12, now), nil)
	expectReleasedSequences(t, cache.add(orderedTestMessage(sessions[2], 12), 12, now), []int{12})

	first := cache.streams[orderingStreamKey{Address: "10.0.0.1", Port: 5000, SessionID: sessions[0]}]
	if cache.held != 2 || len(first.held) != 0 {
		t.Fatalf("global ordering limit mismatch: expected oldest stream released with two messages retained, received held=%d oldest=%d", cache.held, len(first.held))
	}
	if snapshot := stats.Snapshot(); snapshot.OrderingCapacityReleases != 1 {
		t.Fatalf("global ordering stats mismatch: expected one capacity release, received %d", snapshot.OrderingCapacityReleases)
	}
}

func TestResolveRetryConfigOrderingValues(t *testing.T) {
	config, err := ResolveRetryConfig(RetryConfig{})
	if err != nil {
		t.Fatalf("resolve retry config failed: expected nil error, received %v", err)
	}
	if config.OrderedDelivery {
		t.Fatalf("ordered delivery default mismatch: expected disabled, received enabled")
	}
	if config.OrderingHoldTimeout != 10*time.Second {
		t.Fatalf("ordering hold timeout default mismatch: expected %v, received %v", 10*time.Second, config.OrderingHoldTimeout)
	}

	config, err = ResolveRetryConfig(RetryConfig{OrderedDelivery: true, OrderingHoldTimeout: time.Second})
	if err != nil {
		t.Fatalf("resolve ordered retry config failed: expected nil error, received %v", err)
	}
	if !config.OrderedDelivery || config.OrderingHoldTimeout != time.Second {
		t.Fatalf("ordered config mismatch: expected enabled with %v hold timeout, received %v with %v", time.Second, config.OrderedDelivery, config.OrderingHoldTimeout)
	}

	_, err = ResolveRetryConfig(RetryConfig{OrderingHoldTimeout: -1})
	if err == nil {
		t.Fatalf("negative ordering hold timeout mismatch: expected error, received nil")
	}
}

// TestRetryServerOrderedDeliveryReordersWireArrivals proves the hold-back
// queue end to end: an out-of-order packet is acknowledged but held, its
// retransmission is suppressed as a duplicate, and delivery to the
// application resumes in sequence order once the gap fills.
func TestRetryServerOrderedDeliveryReordersWireArrivals(t *testing.T) {
	delivered := make(chan UdpMessage, 8)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, OrderedDelivery: true, OrderingHoldTimeout: time.Minute, DuplicateTTL: time.Minute}, func(incoming, outgoing chan UdpMessage) {
		for message := range incoming {
			delivered <- message
		}
	})
	if err != nil {
		t.Fatalf("start ordered receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)

	host, port := splitHostPort(t, receiver.LocalAddress())
	peer := listenLocalUDP(t)
	defer closeUDPConn(t, peer)
	session := SessionID("00112233445566778899aabbccddeeff")
	message := func(sequence int, data string) UdpMessage {
		return UdpMessage{Data: []byte(data), Address: host, Port: port, Sequence: sequence, Version: ProtocolV1, SessionID: session, MessageID: MessageID(fmt.Sprintf("%032x", sequence))}
	}

	writeTestMessage(t, peer, message(100, "first"))
	expectAck(t, peer, 100)
	first := receiveMessage(t, delivered)
	if string(first.Data) != "first" {
		t.Fatalf("first delivery mismatch: expected %q, received %q", "first", string(first.Data))
	}

	writeTestMessage(t, peer, message(102, "third"))
	expectAck(t, peer, 102)
	select {
	case early := <-delivered:
		t.Fatalf("hold-back mismatch: expected no delivery before the gap fills, received %q", string(early.Data))
	case <-time.After(200 * time.Millisecond):
	}

	writeTestMessage(t, peer, message(102, "third"))
	expectAck(t, peer, 102)
	waitForStats(t, receiver, func(snapshot DeliveryStatsSnapshot) bool {
		return snapshot.Duplicates >= 1
	})

	writeTestMessage(t, peer, message(101, "second"))
	expectAck(t, peer, 101)
	second := receiveMessage(t, delivered)
	if string(second.Data) != "second" {
		t.Fatalf("gap delivery mismatch: expected %q, received %q", "second", string(second.Data))
	}
	third := receiveMessage(t, delivered)
	if string(third.Data) != "third" {
		t.Fatalf("released delivery mismatch: expected %q, received %q", "third", string(third.Data))
	}

	select {
	case extra := <-delivered:
		t.Fatalf("duplicate delivery mismatch: expected no fourth delivery, received %q", string(extra.Data))
	default:
	}
}

func TestRetryServerOrderedDeliveryReleasesGapAfterTimeout(t *testing.T) {
	delivered := make(chan UdpMessage, 8)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, OrderedDelivery: true, OrderingHoldTimeout: 150 * time.Millisecond}, func(incoming, outgoing chan UdpMessage) {
		for message := range incoming {
			delivered <- message
		}
	})
	if err != nil {
		t.Fatalf("start ordered receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)

	host, port := splitHostPort(t, receiver.LocalAddress())
	peer := listenLocalUDP(t)
	defer closeUDPConn(t, peer)
	session := SessionID("00112233445566778899aabbccddeeff")
	message := func(sequence int, data string) UdpMessage {
		return UdpMessage{Data: []byte(data), Address: host, Port: port, Sequence: sequence, Version: ProtocolV1, SessionID: session, MessageID: MessageID(fmt.Sprintf("%032x", sequence))}
	}

	writeTestMessage(t, peer, message(200, "start"))
	expectAck(t, peer, 200)
	start := receiveMessage(t, delivered)
	if string(start.Data) != "start" {
		t.Fatalf("start delivery mismatch: expected %q, received %q", "start", string(start.Data))
	}

	writeTestMessage(t, peer, message(202, "released after timeout"))
	expectAck(t, peer, 202)
	released := receiveMessage(t, delivered)
	if string(released.Data) != "released after timeout" || released.Sequence != 202 {
		t.Fatalf("timeout release mismatch: expected sequence %d %q, received sequence %d %q", 202, "released after timeout", released.Sequence, string(released.Data))
	}

	writeTestMessage(t, peer, message(201, "late"))
	expectAck(t, peer, 201)
	late := receiveMessage(t, delivered)
	if string(late.Data) != "late" {
		t.Fatalf("late delivery mismatch: expected %q, received %q", "late", string(late.Data))
	}
}

// TestRetryServerOrderedDeliveryAssemblesFragmentsIntoSequenceGap proves that
// a reassembled message covers its fragments' whole sequence range: a later
// message held behind the group is released as soon as the group completes.
func TestRetryServerOrderedDeliveryAssemblesFragmentsIntoSequenceGap(t *testing.T) {
	delivered := make(chan UdpMessage, 8)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, OrderedDelivery: true, OrderingHoldTimeout: time.Minute}, func(incoming, outgoing chan UdpMessage) {
		for message := range incoming {
			delivered <- message
		}
	})
	if err != nil {
		t.Fatalf("start ordered receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)

	host, port := splitHostPort(t, receiver.LocalAddress())
	peer := listenLocalUDP(t)
	defer closeUDPConn(t, peer)
	session := SessionID("00112233445566778899aabbccddeeff")
	group := "ffeeddccbbaa99887766554433221100"
	message := func(sequence int, data string) UdpMessage {
		return UdpMessage{Data: []byte(data), Address: host, Port: port, Sequence: sequence, Version: ProtocolV1, SessionID: session, MessageID: MessageID(fmt.Sprintf("%032x", sequence))}
	}
	fragment := func(sequence, index int, data string) UdpMessage {
		part := message(sequence, data)
		part.FragmentGroup = group
		part.FragmentIndex = index
		part.FragmentCount = 3
		return part
	}

	writeTestMessage(t, peer, message(300, "start"))
	expectAck(t, peer, 300)
	start := receiveMessage(t, delivered)
	if string(start.Data) != "start" {
		t.Fatalf("start delivery mismatch: expected %q, received %q", "start", string(start.Data))
	}

	writeTestMessage(t, peer, message(304, "tail"))
	expectAck(t, peer, 304)
	writeTestMessage(t, peer, fragment(303, 2, "CC"))
	expectAck(t, peer, 303)
	writeTestMessage(t, peer, fragment(301, 0, "AA"))
	expectAck(t, peer, 301)
	select {
	case early := <-delivered:
		t.Fatalf("hold-back mismatch: expected no delivery before the group completes, received %q", string(early.Data))
	case <-time.After(200 * time.Millisecond):
	}
	writeTestMessage(t, peer, fragment(302, 1, "BB"))
	expectAck(t, peer, 302)

	assembled := receiveMessage(t, delivered)
	if string(assembled.Data) != "AABBCC" {
		t.Fatalf("assembled delivery mismatch: expected %q, received %q", "AABBCC", string(assembled.Data))
	}
	if assembled.Sequence != 303 || assembled.MessageID != MessageID(group) {
		t.Fatalf("assembled identity mismatch: expected sequence %d message id %q, received sequence %d message id %q", 303, group, assembled.Sequence, assembled.MessageID)
	}
	tail := receiveMessage(t, delivered)
	if string(tail.Data) != "tail" {
		t.Fatalf("tail delivery mismatch: expected %q, received %q", "tail", string(tail.Data))
	}
}

func TestRetryServersOrderedDeliveryPreservesSendOrder(t *testing.T) {
	delivered := make(chan UdpMessage, 8)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16, OrderedDelivery: true}, func(incoming, outgoing chan UdpMessage) {
		for message := range incoming {
			delivered <- message
		}
	})
	if err != nil {
		t.Fatalf("start ordered receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)

	sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 16}, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start ordered sender failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, sender)

	payloads := []string{"one", "two", "three"}
	deliveries := make([]*Delivery, 0, len(payloads))
	for _, payload := range payloads {
		delivery, err := sender.Send(context.Background(), SendRequest{Data: []byte(payload), Target: receiver.LocalEndpoint()})
		if err != nil {
			t.Fatalf("send %q failed: expected nil error, received %v", payload, err)
		}
		deliveries = append(deliveries, delivery)
	}

	for _, payload := range payloads {
		message := receiveMessage(t, delivered)
		if string(message.Data) != payload {
			t.Fatalf("ordered delivery mismatch: expected %q, received %q", payload, string(message.Data))
		}
	}
	for _, delivery := range deliveries {
		result, err := delivery.Wait(context.Background())
		if err != nil {
			t.Fatalf("await ordered delivery failed: expected nil error, received %v", err)
		}
		if result.Status != DeliveryAcknowledged {
			t.Fatalf("ordered delivery status mismatch: expected %q, received %q", DeliveryAcknowledged, result.Status)
		}
	}
}
