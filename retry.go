package brk

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

func StartRetryUdpContext(ctx context.Context, hostName, portNum string, config RetryConfig, processor func(incoming, outgoing chan UdpMessage)) (*RetryUdpServer, error) {
	if ctx == nil {
		return nil, fmt.Errorf("start retry UDP server failed: expected non-nil context")
	}

	resolved, err := ResolveRetryConfig(config)
	if err != nil {
		return nil, err
	}

	seenCache := map[int]time.Time{}
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	appincoming := make(chan UdpMessage, resolved.QueueLength)
	appoutgoing := make(chan UdpMessage, resolved.QueueLength)
	stats := NewDeliveryStats()
	retryCtx, retryCancel := context.WithCancel(ctx)

	retryProcessor := func(netincoming, netoutgoing chan UdpMessage) {
		go retryCachedMessages(retryCtx, resolved, cache, &cacheLock, netoutgoing, stats)
		go forwardRetryIncoming(retryCtx, resolved, seenCache, cache, &cacheLock, appincoming, netincoming, netoutgoing, stats)
		forwardRetryOutgoing(retryCtx, resolved, cache, &cacheLock, appoutgoing, netoutgoing)
	}

	network, err := startUdpContext(retryCtx, hostName, portNum, resolved.QueueLength, stats, retryProcessor)
	if err != nil {
		retryCancel()
		return nil, err
	}

	go processor(appincoming, appoutgoing)
	return &RetryUdpServer{Incoming: appincoming, Outgoing: appoutgoing, Network: network, Config: resolved, cancel: retryCancel}, nil
}

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
			retransmitExpiredMessages(cache, cacheLock, netoutgoing, now, config, stats)
		}
	}
}

func forwardRetryIncoming(ctx context.Context, config RetryConfig, seenCache map[int]time.Time, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, appincoming, netincoming, netoutgoing chan UdpMessage, stats *DeliveryStats) {
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-netincoming:
			if !ok {
				return
			}
			handleRetryIncoming(message, seenCache, cache, cacheLock, config, appincoming, netoutgoing, stats, time.Now())
		}
	}
}

func forwardRetryOutgoing(ctx context.Context, config RetryConfig, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, appoutgoing, netoutgoing chan UdpMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-appoutgoing:
			if !ok {
				return
			}
			queueOutgoingMessage(message, cache, cacheLock, netoutgoing, config)
		}
	}
}

func retransmitExpiredMessages(cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, now time.Time, config RetryConfig, stats *DeliveryStats) int {
	keys := cachedSequences(cache, cacheLock)
	retransmitted := 0
	for _, key := range keys {
		cacheLock.Lock()
		entry, ok := cache[key]
		cacheLock.Unlock()
		if ok && now.Sub(entry.Message.Cached) > config.AckTimeout {
			if config.MaxAttempts > 0 && entry.Attempts >= config.MaxAttempts {
				cacheLock.Lock()
				delete(cache, key)
				cacheLock.Unlock()
				stats.addDropped()
				logDropped(entry.Message, entry.Attempts)
			} else {
				entry.Attempts = entry.Attempts + 1
				entry.Message.Cached = now
				cacheLock.Lock()
				cache[entry.Message.Sequence] = entry
				cacheLock.Unlock()
				netoutgoing <- entry.Message
				stats.addRetried()
				retransmitted = retransmitted + 1
			}
		}
	}

	if retransmitted > 0 {
		logRetransmitted(retransmitted)
	}

	return retransmitted
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

func handleRetryIncoming(message UdpMessage, seenCache map[int]time.Time, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, config RetryConfig, appincoming, netoutgoing chan UdpMessage, stats *DeliveryStats, now time.Time) {
	if message.Type == ackType {
		cacheLock.Lock()
		delete(cache, message.Sequence)
		cacheLock.Unlock()
		stats.addAcked()
	} else {
		netoutgoing <- UdpMessage{Data: []byte{}, Address: message.Address, Port: message.Port, Sequence: message.Sequence, Type: ackType, Cached: now}
		if rememberIncomingSequence(message.Sequence, seenCache, config.DuplicateTTL, now) {
			appincoming <- message
		} else {
			stats.addDuplicate()
			logDuplicate(message)
		}
	}
}

func rememberIncomingSequence(sequence int, seenCache map[int]time.Time, duplicateTTL time.Duration, now time.Time) bool {
	pruneSeenCache(seenCache, duplicateTTL, now)
	_, exists := seenCache[sequence]
	if exists {
		return false
	} else {
		seenCache[sequence] = now
		return true
	}
}

func pruneSeenCache(seenCache map[int]time.Time, duplicateTTL time.Duration, now time.Time) {
	for sequence, seenAt := range seenCache {
		if now.Sub(seenAt) > duplicateTTL {
			delete(seenCache, sequence)
		}
	}
}

func queueOutgoingMessage(message UdpMessage, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, config RetryConfig) {
	message.Sequence = getSequence()
	message.Cached = time.Now()
	cacheLock.Lock()
	cache[message.Sequence] = retryCacheEntry{Message: message}
	cacheLock.Unlock()
	netoutgoing <- message
}
