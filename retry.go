package brk

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"
)

// StartRetryUdpContext starts a retrying UDP server and returns a closeable handle.
func StartRetryUdpContext(ctx context.Context, hostName, portNum string, config RetryConfig, processor func(incoming, outgoing chan UdpMessage)) (*RetryUdpServer, error) {
	if ctx == nil {
		return nil, fmt.Errorf("start retry UDP server failed: expected non-nil context")
	}
	if processor == nil {
		return nil, fmt.Errorf("start retry UDP server failed: expected non-nil processor")
	}

	resolved, err := ResolveRetryConfig(config)
	if err != nil {
		return nil, err
	}

	seenCache := map[receivedMessageKey]time.Time{}
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	appincoming := make(chan UdpMessage, resolved.QueueLength)
	appoutgoing := make(chan UdpMessage, resolved.QueueLength)
	stats := NewDeliveryStats()
	retryCtx, retryCancel := context.WithCancel(ctx)
	retryDone := make(chan struct{})

	retryProcessor := func(netincoming, netoutgoing chan UdpMessage) {
		defer close(retryDone)
		retryTasks := sync.WaitGroup{}
		retryTasks.Add(2)
		go func() {
			defer retryTasks.Done()
			retryCachedMessages(retryCtx, resolved, cache, &cacheLock, netoutgoing, stats)
		}()
		go func() {
			defer retryTasks.Done()
			forwardRetryIncoming(retryCtx, resolved, seenCache, cache, &cacheLock, appincoming, netincoming, netoutgoing, stats)
		}()
		forwardRetryOutgoing(retryCtx, cache, &cacheLock, appoutgoing, netoutgoing, stats)
		retryTasks.Wait()
	}

	network, err := startUdpContext(retryCtx, hostName, portNum, resolved.QueueLength, stats, retryProcessor)
	if err != nil {
		retryCancel()
		return nil, err
	}

	go processor(appincoming, appoutgoing)
	done := make(chan struct{})
	go func() {
		<-network.Done()
		retryCancel()
		<-retryDone
		close(done)
	}()
	return &RetryUdpServer{Incoming: appincoming, Outgoing: appoutgoing, Network: network, Config: resolved, cancel: retryCancel, done: done}, nil
}

// StartRetryUdp starts a retrying UDP server with default settings and blocks until it stops.
func StartRetryUdp(hostName, portNum string, processor func(a, b chan UdpMessage)) (chan UdpMessage, chan UdpMessage) {
	server, err := StartRetryUdpContext(context.Background(), hostName, portNum, RetryConfig{}, processor)
	if err != nil {
		panic(err)
	}
	<-server.Done()
	return server.Incoming, server.Outgoing
}

func retryCachedMessages(ctx context.Context, config RetryConfig, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, stats *DeliveryStats) {
	ticker := time.NewTicker(config.RetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			retransmitExpiredMessages(ctx, cache, cacheLock, netoutgoing, now, config, stats)
		}
	}
}

func forwardRetryIncoming(ctx context.Context, config RetryConfig, seenCache map[receivedMessageKey]time.Time, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, appincoming, netincoming, netoutgoing chan UdpMessage, stats *DeliveryStats) {
	defer close(appincoming)
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-netincoming:
			if !ok {
				return
			}
			if !handleRetryIncoming(ctx, message, seenCache, cache, cacheLock, config, appincoming, netoutgoing, stats, time.Now()) {
				return
			}
		}
	}
}

func forwardRetryOutgoing(ctx context.Context, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, appoutgoing, netoutgoing chan UdpMessage, stats *DeliveryStats) {
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-appoutgoing:
			if !ok {
				return
			}
			if !queueOutgoingMessage(ctx, message, cache, cacheLock, netoutgoing, stats) {
				return
			}
		}
	}
}

func retransmitExpiredMessages(ctx context.Context, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, now time.Time, config RetryConfig, stats *DeliveryStats) int {
	keys := cachedSequences(cache, cacheLock)
	retransmitted := 0
	for _, key := range keys {
		message, attempts, retry, dropped := prepareExpiredMessage(cache, cacheLock, key, now, config)
		if dropped {
			stats.addDropped()
			logDropped(message, attempts)
		} else if retry {
			select {
			case netoutgoing <- message:
				stats.addRetried()
				retransmitted = retransmitted + 1
			case <-ctx.Done():
				return retransmitted
			}
		}
	}

	if retransmitted > 0 {
		logRetransmitted(retransmitted)
	}

	return retransmitted
}

func prepareExpiredMessage(cache map[int]retryCacheEntry, cacheLock *sync.Mutex, key int, now time.Time, config RetryConfig) (UdpMessage, int, bool, bool) {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	entry, ok := cache[key]
	if !ok || now.Sub(entry.Message.Cached) <= config.AckTimeout {
		return UdpMessage{}, 0, false, false
	}
	if config.MaxAttempts > 0 && entry.Attempts >= config.MaxAttempts {
		delete(cache, key)
		return entry.Message, entry.Attempts, false, true
	}
	entry.Attempts = entry.Attempts + 1
	entry.Message.Cached = now
	cache[key] = entry
	return entry.Message, entry.Attempts, true, false
}

func cachedSequences(cache map[int]retryCacheEntry, cacheLock *sync.Mutex) []int {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	keys := make([]int, 0, len(cache))
	for key := range cache {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	return keys
}

func handleRetryIncoming(ctx context.Context, message UdpMessage, seenCache map[receivedMessageKey]time.Time, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, config RetryConfig, appincoming, netoutgoing chan UdpMessage, stats *DeliveryStats, now time.Time) bool {
	if message.Type == ackType {
		if removeAcknowledgedMessage(message, cache, cacheLock) {
			stats.addAcked()
		}
		return true
	}

	newMessage := rememberIncomingMessage(message, seenCache, config.DuplicateTTL, now)
	if newMessage {
		select {
		case appincoming <- message:
		case <-ctx.Done():
			return false
		}
	} else {
		stats.addDuplicate()
		logDuplicate(message)
	}

	acknowledgement := UdpMessage{Data: []byte{}, Address: message.Address, Port: message.Port, Sequence: message.Sequence, Type: ackType, Cached: now}
	select {
	case netoutgoing <- acknowledgement:
		return true
	case <-ctx.Done():
		return false
	}
}

func removeAcknowledgedMessage(message UdpMessage, cache map[int]retryCacheEntry, cacheLock *sync.Mutex) bool {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	entry, exists := cache[message.Sequence]
	if !exists || !acknowledgementMatchesTarget(message, entry.Message) {
		return false
	}
	delete(cache, message.Sequence)
	return true
}

func acknowledgementMatchesTarget(acknowledgement, outgoing UdpMessage) bool {
	if acknowledgement.Port != outgoing.Port {
		return false
	}
	targetIP := net.ParseIP(outgoing.Address)
	if targetIP == nil {
		return true
	}
	sourceIP := net.ParseIP(acknowledgement.Address)
	return sourceIP != nil && targetIP.Equal(sourceIP)
}

func rememberIncomingMessage(message UdpMessage, seenCache map[receivedMessageKey]time.Time, duplicateTTL time.Duration, now time.Time) bool {
	pruneSeenCache(seenCache, duplicateTTL, now)
	key := receivedMessageKey{Address: message.Address, Port: message.Port, Sequence: message.Sequence}
	_, exists := seenCache[key]
	if exists {
		return false
	} else {
		seenCache[key] = now
		return true
	}
}

func pruneSeenCache(seenCache map[receivedMessageKey]time.Time, duplicateTTL time.Duration, now time.Time) {
	for key, seenAt := range seenCache {
		if now.Sub(seenAt) > duplicateTTL {
			delete(seenCache, key)
		}
	}
}

func queueOutgoingMessage(ctx context.Context, message UdpMessage, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, stats *DeliveryStats) bool {
	message.Sequence = getSequence()
	message.Cached = time.Now()
	_, err := encodeUDPMessage(message)
	if err != nil {
		stats.addFailedWrite()
		logUDPWriteFailure(message, err)
		return true
	}
	cacheLock.Lock()
	cache[message.Sequence] = retryCacheEntry{Message: message}
	cacheLock.Unlock()
	select {
	case netoutgoing <- message:
		return true
	case <-ctx.Done():
		return false
	}
}
