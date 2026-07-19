package brk

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFragmentOutgoingMessageSplitsAndPreservesOrder(t *testing.T) {
	data := bytes.Repeat([]byte("abcd"), 100)
	message := UdpMessage{Data: data, Address: "127.0.0.1", Port: 9000}

	fragments, err := fragmentOutgoingMessage(message, 100)
	if err != nil {
		t.Fatalf("fragment message failed: expected nil error, received %v", err)
	}
	if len(fragments) != 4 {
		t.Fatalf("fragment count mismatch: expected %d, received %d", 4, len(fragments))
	}

	reassembled := make([]byte, 0, len(data))
	group := fragments[0].FragmentGroup
	for index, fragment := range fragments {
		if fragment.FragmentGroup != group {
			t.Fatalf("fragment group mismatch at %d: expected %q, received %q", index, group, fragment.FragmentGroup)
		}
		if fragment.FragmentIndex != index {
			t.Fatalf("fragment index mismatch: expected %d, received %d", index, fragment.FragmentIndex)
		}
		if fragment.FragmentCount != 4 {
			t.Fatalf("fragment count field mismatch at %d: expected %d, received %d", index, 4, fragment.FragmentCount)
		}
		reassembled = append(reassembled, fragment.Data...)
	}
	if !bytes.Equal(reassembled, data) {
		t.Fatalf("fragment payload mismatch: expected %d bytes to reassemble to the original", len(data))
	}
}

func TestFragmentOutgoingMessageCopiesPayloadSlices(t *testing.T) {
	data := bytes.Repeat([]byte("z"), 10)
	message := UdpMessage{Data: data, Address: "127.0.0.1", Port: 9000}

	fragments, err := fragmentOutgoingMessage(message, 4)
	if err != nil {
		t.Fatalf("fragment message failed: expected nil error, received %v", err)
	}
	data[0] = 'x'
	if fragments[0].Data[0] != 'z' {
		t.Fatalf("fragment payload ownership mismatch: expected fragment to keep %q after caller mutation, received %q", "z", string(fragments[0].Data[0]))
	}
}

func TestFragmentedMessageRejectsBeforeQueueingWhenEveryFragmentCannotFit(t *testing.T) {
	config := fastRetryConfig()
	config.FragmentPayloadBytes = 4
	config.MaxPending = 2
	messageID := MessageID("ffeeddccbbaa99887766554433221100")
	delivery := newDelivery(messageID)
	request := outboundRequest{Message: UdpMessage{Data: []byte("eightbit"), Address: "127.0.0.1", Port: 9000, MessageID: messageID}, Delivery: delivery}
	cache := map[int]retryCacheEntry{41: {Message: UdpMessage{Sequence: 41}}}
	cacheLock := sync.Mutex{}
	networkOutgoing := make(chan UdpMessage, 2)
	stats := NewDeliveryStats()

	if continued := queueOutgoingRequest(context.Background(), config, SessionID("00112233445566778899aabbccddeeff"), request, cache, &cacheLock, networkOutgoing, stats); !continued {
		t.Fatalf("fragmented pending rejection stopped forwarding: expected true, received false")
	}
	result, err := delivery.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for fragmented pending rejection failed: expected nil error, received %v", err)
	}
	if result.Status != DeliveryRejected || result.Reason != DeliveryReasonPendingLimit {
		t.Fatalf("fragmented pending result mismatch: expected rejected/pending_limit, received %+v", result)
	}
	if !strings.Contains(result.WriteError, "expected 2 free pending slots") || !strings.Contains(result.WriteError, "available 1 of limit 2 with 1 pending messages") {
		t.Fatalf("fragmented pending error mismatch: expected required and available capacity, received %q", result.WriteError)
	}
	if len(cache) != 1 {
		t.Fatalf("fragmented pending cache mismatch: expected the existing entry only, received %d entries", len(cache))
	}
	select {
	case fragment := <-networkOutgoing:
		t.Fatalf("fragmented pending dispatch mismatch: expected no partial group, received sequence=%d index=%d count=%d", fragment.Sequence, fragment.FragmentIndex, fragment.FragmentCount)
	default:
	}
	if stats.Snapshot().Rejected != 1 {
		t.Fatalf("fragmented pending rejection count mismatch: expected 1, received %d", stats.Snapshot().Rejected)
	}
}

func TestReassemblyCacheAssemblesOutOfOrderFragments(t *testing.T) {
	cache := newReassemblyCache()
	now := time.Now()
	data := bytes.Repeat([]byte("payload"), 50)
	message := UdpMessage{Data: data, Address: "10.0.0.1", Port: 5000, SessionID: SessionID("00112233445566778899aabbccddeeff")}
	fragments, err := fragmentOutgoingMessage(message, 60)
	if err != nil {
		t.Fatalf("fragment message failed: expected nil error, received %v", err)
	}
	for index := range fragments {
		fragments[index].Address = message.Address
		fragments[index].Port = message.Port
		fragments[index].SessionID = message.SessionID
		fragments[index].Sequence = 100 + index
	}

	order := []int{2, 0, len(fragments) - 1}
	for _, at := range order {
		if at == len(fragments)-1 {
			continue
		}
		_, _, complete, err := cache.add(fragments[at], time.Minute, now)
		if err != nil {
			t.Fatalf("add fragment %d failed: expected nil error, received %v", at, err)
		}
		if complete {
			t.Fatalf("premature completion mismatch: expected group to remain incomplete at fragment %d", at)
		}
	}

	var assembled UdpMessage
	spanStart := 0
	completed := false
	for index := range fragments {
		if index == 0 || index == 2 {
			continue
		}
		message, start, complete, err := cache.add(fragments[index], time.Minute, now)
		if err != nil {
			t.Fatalf("add fragment %d failed: expected nil error, received %v", index, err)
		}
		if complete {
			assembled = message
			spanStart = start
			completed = true
		}
	}
	if !completed {
		t.Fatalf("reassembly completion mismatch: expected the group to complete")
	}
	if !bytes.Equal(assembled.Data, data) {
		t.Fatalf("reassembled payload mismatch: expected %d bytes to match the original", len(data))
	}
	if assembled.FragmentCount != 0 || assembled.FragmentGroup != "" {
		t.Fatalf("assembled fragment fields mismatch: expected cleared fragment metadata, received group=%q count=%d", assembled.FragmentGroup, assembled.FragmentCount)
	}
	if assembled.MessageID != MessageID(fragments[0].FragmentGroup) {
		t.Fatalf("assembled message id mismatch: expected %q, received %q", fragments[0].FragmentGroup, assembled.MessageID)
	}
	if assembled.Sequence != 100+len(fragments)-1 {
		t.Fatalf("assembled sequence mismatch: expected the group's highest sequence %d, received %d", 100+len(fragments)-1, assembled.Sequence)
	}
	if spanStart != 100 {
		t.Fatalf("assembled span start mismatch: expected the group's lowest sequence %d, received %d", 100, spanStart)
	}
}

func TestReassemblyCacheRejectsInconsistentCount(t *testing.T) {
	cache := newReassemblyCache()
	now := time.Now()
	group := "00112233445566778899aabbccddeeff"
	first := UdpMessage{Data: []byte("a"), Address: "10.0.0.1", Port: 5000, SessionID: SessionID(group), FragmentGroup: group, FragmentIndex: 0, FragmentCount: 3}
	conflicting := UdpMessage{Data: []byte("b"), Address: "10.0.0.1", Port: 5000, SessionID: SessionID(group), FragmentGroup: group, FragmentIndex: 1, FragmentCount: 4}

	if _, _, _, err := cache.add(first, time.Minute, now); err != nil {
		t.Fatalf("add first fragment failed: expected nil error, received %v", err)
	}
	if _, _, _, err := cache.add(conflicting, time.Minute, now); err == nil {
		t.Fatalf("inconsistent count mismatch: expected error, received nil")
	}
}

func TestReassemblyCacheExpiresPartialGroups(t *testing.T) {
	cache := newReassemblyCache()
	start := time.Now()
	group := "00112233445566778899aabbccddeeff"
	first := UdpMessage{Data: []byte("a"), Address: "10.0.0.1", Port: 5000, SessionID: SessionID(group), FragmentGroup: group, FragmentIndex: 0, FragmentCount: 2}

	if _, _, _, err := cache.add(first, time.Second, start); err != nil {
		t.Fatalf("add first fragment failed: expected nil error, received %v", err)
	}
	cache.prune(time.Second, start.Add(2*time.Second))
	if len(cache.groups) != 0 {
		t.Fatalf("expired reassembly mismatch: expected the stale partial group to be pruned, received %d groups", len(cache.groups))
	}
}

func TestRetryServersDeliverLargeFragmentedMessage(t *testing.T) {
	for _, version := range []ProtocolVersion{ProtocolV1, ProtocolV2} {
		version := version
		t.Run(wireVersionName(version), func(t *testing.T) {
			delivered := make(chan UdpMessage, 1)
			receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 64, WireVersion: version}, func(incoming, outgoing chan UdpMessage) {
				for message := range incoming {
					delivered <- message
				}
			})
			if err != nil {
				t.Fatalf("start fragment receiver failed: expected nil error, received %v", err)
			}
			defer closeRetryServer(t, receiver)

			sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", RetryConfig{QueueLength: 64, WireVersion: version}, func(incoming, outgoing chan UdpMessage) {})
			if err != nil {
				t.Fatalf("start fragment sender failed: expected nil error, received %v", err)
			}
			defer closeRetryServer(t, sender)

			payload := make([]byte, 100000)
			for index := range payload {
				payload[index] = byte(index)
			}

			result, err := sender.Send(context.Background(), SendRequest{Data: payload, Target: receiver.LocalEndpoint()})
			if err != nil {
				t.Fatalf("send fragmented message failed: expected nil error, received %v", err)
			}

			message := receiveMessage(t, delivered)
			if !bytes.Equal(message.Data, payload) {
				t.Fatalf("fragmented delivery mismatch: expected %d bytes to match the original, received %d", len(payload), len(message.Data))
			}

			terminal, err := result.Wait(context.Background())
			if err != nil {
				t.Fatalf("await fragmented delivery failed: expected nil error, received %v", err)
			}
			if terminal.Status != DeliveryAcknowledged {
				t.Fatalf("fragmented delivery status mismatch: expected %q, received %q", DeliveryAcknowledged, terminal.Status)
			}
		})
	}
}

func wireVersionName(version ProtocolVersion) string {
	if version == ProtocolV2 {
		return "v2"
	}
	return "v1"
}
