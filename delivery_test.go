package brk

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRetryServerSendReturnsAcknowledgedDelivery(t *testing.T) {
	config := fastRetryConfig()
	received := make(chan UdpMessage, 1)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", config, func(incoming, outgoing chan UdpMessage) {
		received <- <-incoming
	})
	if err != nil {
		t.Fatalf("start retry receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)
	sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", config, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start retry sender failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, sender)

	delivery, err := sender.Send(context.Background(), SendRequest{Data: []byte("receipt"), Target: receiver.LocalEndpoint()})
	if err != nil {
		t.Fatalf("send retry message failed: expected nil error, received %v", err)
	}
	result, err := delivery.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for acknowledged delivery failed: expected nil error, received %v", err)
	}
	if result.MessageID != delivery.ID() || result.SessionID != sender.SessionID() {
		t.Fatalf("delivery identity mismatch: expected session=%s message=%s, received session=%s message=%s", sender.SessionID(), delivery.ID(), result.SessionID, result.MessageID)
	}
	if result.Status != DeliveryAcknowledged || result.Reason != DeliveryReasonAcknowledgement || result.Attempts != 1 {
		t.Fatalf("delivery result mismatch: expected acknowledged/acknowledgement with one attempt, received %+v", result)
	}
	if result.Target != receiver.LocalEndpoint() || result.Latency < 0 {
		t.Fatalf("delivery target or latency mismatch: expected target=%v nonnegative latency, received target=%v latency=%v", receiver.LocalEndpoint(), result.Target, result.Latency)
	}
	if message := receiveMessage(t, received); string(message.Data) != "receipt" || message.MessageID != delivery.ID() {
		t.Fatalf("received delivery mismatch: expected data=%q message=%s, received data=%q message=%s", "receipt", delivery.ID(), message.Data, message.MessageID)
	}
}

func TestRetryServerAuthenticationMismatchExpiresDelivery(t *testing.T) {
	receiverConfig := fastRetryConfig()
	receiverConfig.AuthenticationKey = bytes.Repeat([]byte("r"), minimumSharedKeyBytes)
	senderConfig := fastRetryConfig()
	senderConfig.AuthenticationKey = bytes.Repeat([]byte("s"), minimumSharedKeyBytes)
	receiver, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", receiverConfig, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start authenticated retry receiver failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, receiver)
	sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", senderConfig, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start authenticated retry sender failed: expected nil error, received %v", err)
	}
	defer closeRetryServer(t, sender)

	delivery, err := sender.Send(context.Background(), SendRequest{Data: []byte("private"), Target: receiver.LocalEndpoint(), Deadline: time.Now().Add(100 * time.Millisecond)})
	if err != nil {
		t.Fatalf("send authenticated retry message failed: expected nil error, received %v", err)
	}
	result, err := delivery.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for unauthenticated delivery failed: expected nil error, received %v", err)
	}
	if result.Status != DeliveryExpired || result.Reason != DeliveryReasonDeadline {
		t.Fatalf("authentication mismatch delivery result: expected expired/deadline, received %+v", result)
	}
	if receiver.Stats().AuthenticationFailures == 0 {
		t.Fatalf("authentication failure stats mismatch: expected at least one failure, received %+v", receiver.Stats())
	}
}

func TestRetryServerPendingLimitRejectsSecondDelivery(t *testing.T) {
	receiver, err := StartUdpContext(context.Background(), "127.0.0.1", "0", func(incoming, outgoing chan UdpMessage) {
		for range incoming {
		}
	})
	if err != nil {
		t.Fatalf("start raw receiver failed: expected nil error, received %v", err)
	}
	defer closeUdpServer(t, receiver)
	config := fastRetryConfig()
	config.MaxPending = 1
	sender, err := StartRetryUdpContext(context.Background(), "127.0.0.1", "0", config, func(incoming, outgoing chan UdpMessage) {})
	if err != nil {
		t.Fatalf("start limited retry sender failed: expected nil error, received %v", err)
	}

	first, err := sender.Send(context.Background(), SendRequest{Data: []byte("first"), Target: receiver.LocalEndpoint()})
	if err != nil {
		t.Fatalf("send first limited message failed: expected nil error, received %v", err)
	}
	second, err := sender.Send(context.Background(), SendRequest{Data: []byte("second"), Target: receiver.LocalEndpoint()})
	if err != nil {
		t.Fatalf("send second limited message failed: expected nil error, received %v", err)
	}
	result, err := second.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for limited delivery failed: expected nil error, received %v", err)
	}
	if result.Status != DeliveryRejected || result.Reason != DeliveryReasonPendingLimit {
		t.Fatalf("pending limit result mismatch: expected rejected/pending_limit, received %+v", result)
	}
	if sender.Stats().Rejected != 1 {
		t.Fatalf("pending limit stats mismatch: expected %d, received %d", 1, sender.Stats().Rejected)
	}
	if err := sender.Close(); err != nil {
		t.Fatalf("close limited retry sender failed: expected nil error, received %v", err)
	}
	firstResult, err := first.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for canceled first delivery failed: expected nil error, received %v", err)
	}
	if firstResult.Status != DeliveryCanceled || firstResult.Reason != DeliveryReasonServerShutdown {
		t.Fatalf("canceled delivery result mismatch: expected canceled/server_shutdown, received %+v", firstResult)
	}
}

func TestPermanentWriteResultFailsDelivery(t *testing.T) {
	now := time.Now()
	delivery := newDelivery(MessageID("ffeeddccbbaa99887766554433221100"))
	message := UdpMessage{Address: "127.0.0.1", Port: 9000, Sequence: 42, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: delivery.ID()}
	cache := map[int]retryCacheEntry{42: {Message: message, AcceptedAt: now, Attempts: 1, InFlight: true, Delivery: delivery}}
	cacheLock := sync.Mutex{}
	stats := NewDeliveryStats()
	handleWriteResult(fastRetryConfig(), cache, &cacheLock, WriteResult{SessionID: message.SessionID, MessageID: message.MessageID, Sequence: 42, Finished: now.Add(time.Millisecond), Error: "write failed", Permanent: true}, stats)

	result, err := delivery.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for failed write delivery failed: expected nil error, received %v", err)
	}
	if result.Status != DeliveryFailed || result.Reason != DeliveryReasonWriteFailure || result.WriteError != "write failed" {
		t.Fatalf("permanent write result mismatch: expected failed/write_failure with exact error, received %+v", result)
	}
	if len(cache) != 0 {
		t.Fatalf("permanent write cache mismatch: expected %d entries, received %d", 0, len(cache))
	}
}

func TestAuthenticationErrorsDoNotExposeKey(t *testing.T) {
	key := bytes.Repeat([]byte("secret"), 6)
	key = key[:minimumSharedKeyBytes]
	message := UdpMessage{Version: ProtocolV1, SessionID: SessionID("00112233445566778899aabbccddeeff"), MessageID: MessageID("ffeeddccbbaa99887766554433221100")}
	err := verifyMessageAuthentication(message, key)
	if err == nil {
		t.Fatalf("missing authentication mismatch: expected error, received nil")
	}
	if strings.Contains(err.Error(), string(key)) {
		t.Fatalf("authentication error disclosed configured key: error=%q", err.Error())
	}
}

func TestDeliveryPublishesOneTerminalResult(t *testing.T) {
	delivery := newDelivery(MessageID("ffeeddccbbaa99887766554433221100"))
	if result, complete := delivery.Result(); complete {
		t.Fatalf("pending delivery result mismatch: expected incomplete zero result, received %+v", result)
	}
	select {
	case <-delivery.Done():
		t.Fatalf("pending delivery done mismatch: expected open channel")
	default:
	}
	first := DeliveryResult{MessageID: delivery.ID(), Status: DeliveryAcknowledged, Reason: DeliveryReasonAcknowledgement}
	completeDelivery(delivery, first)
	completeDelivery(delivery, DeliveryResult{MessageID: delivery.ID(), Status: DeliveryFailed, Reason: DeliveryReasonWriteFailure})
	result, err := delivery.Wait(context.Background())
	if err != nil {
		t.Fatalf("wait for completed delivery failed: expected nil error, received %v", err)
	}
	if result != first {
		t.Fatalf("terminal delivery result mismatch: expected %+v, received %+v", first, result)
	}
}

func TestDeliveryWaitRespectsContext(t *testing.T) {
	delivery := newDelivery(MessageID("ffeeddccbbaa99887766554433221100"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := delivery.Wait(ctx)
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("canceled delivery wait mismatch: expected context cancellation, received %v", err)
	}
}

func fastRetryConfig() RetryConfig {
	return RetryConfig{QueueLength: 20, RetryInterval: 5 * time.Millisecond, AckTimeout: 10 * time.Millisecond, MaxAttempts: 5, DuplicateTTL: time.Second, MaxPending: 20, BackoffMultiplier: 2, MaxRetryDelay: 40 * time.Millisecond, DisableJitter: true, DeliveryTimeout: time.Second}
}
