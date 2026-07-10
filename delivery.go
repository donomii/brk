package brk

import (
	"context"
	"fmt"
	"time"
)

// Send validates and queues a version 1 retry message and returns its delivery handle.
func (server *RetryUdpServer) Send(ctx context.Context, request SendRequest) (*Delivery, error) {
	if ctx == nil {
		return nil, fmt.Errorf("send retry UDP message failed: expected non-nil context")
	}
	target, err := validatePeerFamily(request.Target, server.LocalEndpoint(), "send retry UDP message")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	deadline, err := resolveSendDeadline(request.Deadline, server.Config, now)
	if err != nil {
		return nil, err
	}
	messageID, err := newMessageID()
	if err != nil {
		return nil, err
	}
	delivery := newDelivery(messageID)
	message := UdpMessage{Data: append([]byte(nil), request.Data...), Address: target.Addr().String(), Port: int(target.Port()), Version: ProtocolV1, SessionID: server.sessionID, MessageID: messageID, Deadline: deadline}
	select {
	case server.requests <- outboundRequest{Message: message, Delivery: delivery}:
		return delivery, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("send retry UDP message %q to %v failed before queueing: %w", messageID, target, ctx.Err())
	case <-server.Done():
		return nil, fmt.Errorf("send retry UDP message %q to %v failed: retry server is closed", messageID, target)
	}
}

// SessionID returns the random identity assigned when the retry server started.
func (server *RetryUdpServer) SessionID() SessionID {
	return server.sessionID
}

func newDelivery(messageID MessageID) *Delivery {
	return &Delivery{messageID: messageID, done: make(chan struct{})}
}

// ID returns the delivery's message identity.
func (delivery *Delivery) ID() MessageID {
	return delivery.messageID
}

// Done closes when the delivery reaches its terminal result.
func (delivery *Delivery) Done() <-chan struct{} {
	return delivery.done
}

// Result returns the terminal result and true, or a zero result and false while delivery is pending.
func (delivery *Delivery) Result() (DeliveryResult, bool) {
	select {
	case <-delivery.done:
		delivery.lock.Lock()
		defer delivery.lock.Unlock()
		return delivery.result, true
	default:
		return DeliveryResult{}, false
	}
}

// Wait blocks until delivery completes or ctx ends.
func (delivery *Delivery) Wait(ctx context.Context) (DeliveryResult, error) {
	if ctx == nil {
		return DeliveryResult{}, fmt.Errorf("wait for delivery %q failed: expected non-nil context", delivery.messageID)
	}
	select {
	case <-delivery.done:
		result, _ := delivery.Result()
		return result, nil
	case <-ctx.Done():
		return DeliveryResult{}, fmt.Errorf("wait for delivery %q failed: %w", delivery.messageID, ctx.Err())
	}
}

func completeDelivery(delivery *Delivery, result DeliveryResult) {
	if delivery == nil {
		return
	}
	delivery.completeOnce.Do(func() {
		delivery.lock.Lock()
		delivery.result = result
		delivery.lock.Unlock()
		close(delivery.done)
	})
}

func resolveSendDeadline(requested time.Time, config RetryConfig, now time.Time) (time.Time, error) {
	if !requested.IsZero() {
		if !requested.After(now) {
			return time.Time{}, fmt.Errorf("send retry UDP message failed: expected deadline after %v, received %v", now, requested)
		}
		return requested, nil
	}
	if config.DeliveryTimeout == 0 {
		return time.Time{}, nil
	}
	return now.Add(config.DeliveryTimeout), nil
}
