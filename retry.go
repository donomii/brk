package brk

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}

	seenCache := newReceivedMessageCache()
	cache := map[int]retryCacheEntry{}
	cacheLock := sync.Mutex{}
	appincoming := make(chan UdpMessage, resolved.QueueLength)
	appoutgoing := make(chan UdpMessage, resolved.QueueLength)
	requests := make(chan outboundRequest, resolved.QueueLength)
	writeResults := make(chan WriteResult, resolved.MaxPending)
	stats := NewDeliveryStats()
	retryCtx, retryCancel := context.WithCancel(ctx)
	retryDone := make(chan struct{})

	retryProcessor := func(netincoming, netoutgoing chan UdpMessage) {
		defer close(retryDone)
		retryTasks := sync.WaitGroup{}
		retryTasks.Add(3)
		go func() {
			defer retryTasks.Done()
			retryCachedMessages(retryCtx, resolved, cache, &cacheLock, netoutgoing, stats)
		}()
		go func() {
			defer retryTasks.Done()
			forwardRetryIncoming(retryCtx, resolved, seenCache, cache, &cacheLock, appincoming, netincoming, netoutgoing, stats)
		}()
		go func() {
			defer retryTasks.Done()
			forwardWriteResults(retryCtx, resolved, cache, &cacheLock, writeResults, stats)
		}()
		forwardRetryOutgoing(retryCtx, resolved, sessionID, cache, &cacheLock, appoutgoing, requests, netoutgoing, stats)
		retryTasks.Wait()
		finishPendingDeliveries(cache, &cacheLock, time.Now())
	}

	network, err := startUdpContext(retryCtx, hostName, portNum, resolved.QueueLength, stats, writeResults, retryProcessor)
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
	return &RetryUdpServer{Incoming: appincoming, Outgoing: appoutgoing, Network: network, Config: resolved, requests: requests, sessionID: sessionID, cancel: retryCancel, done: done}, nil
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

func forwardRetryIncoming(ctx context.Context, config RetryConfig, seenCache *receivedMessageCache, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, appincoming, netincoming, netoutgoing chan UdpMessage, stats *DeliveryStats) {
	defer close(appincoming)
	for {
		select {
		case <-ctx.Done():
			return
		case message, ok := <-netincoming:
			if !ok {
				return
			}
			if !validateRetryPacket(message, config, stats) {
				continue
			}
			if !handleRetryIncoming(ctx, message, seenCache, cache, cacheLock, config, appincoming, netoutgoing, stats, time.Now()) {
				return
			}
		}
	}
}

func validateRetryPacket(message UdpMessage, config RetryConfig, stats *DeliveryStats) bool {
	if message.Version == ProtocolV1 {
		err := validateV1Message(message)
		if err != nil {
			stats.addInvalidPacket()
			logInvalidRetryPacket(message, err)
			return false
		}
	} else if message.Version != ProtocolLegacy {
		err := fmt.Errorf("expected protocol version 0 or 1, received %d", message.Version)
		stats.addInvalidPacket()
		logInvalidRetryPacket(message, err)
		return false
	}
	err := verifyMessageAuthentication(message, config.AuthenticationKey)
	if err != nil {
		stats.addAuthenticationFailure()
		logAuthenticationFailure(message, err)
		return false
	}
	return true
}

func forwardRetryOutgoing(ctx context.Context, config RetryConfig, sessionID SessionID, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, appoutgoing chan UdpMessage, requests chan outboundRequest, netoutgoing chan UdpMessage, stats *DeliveryStats) {
	for {
		select {
		case <-ctx.Done():
			return
		case request := <-requests:
			if !queueOutgoingRequest(ctx, config, sessionID, request, cache, cacheLock, netoutgoing, stats) {
				return
			}
		case message, ok := <-appoutgoing:
			if !ok {
				return
			}
			if !queueOutgoingRequest(ctx, config, sessionID, outboundRequest{Message: message}, cache, cacheLock, netoutgoing, stats) {
				return
			}
		}
	}
}

func retransmitExpiredMessages(ctx context.Context, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, now time.Time, config RetryConfig, stats *DeliveryStats) int {
	keys := cachedSequences(cache, cacheLock)
	retransmitted := 0
	for _, key := range keys {
		action := prepareRetryAction(cache, cacheLock, key, now, config)
		if action.Terminal {
			recordRetryTerminal(action, now, stats)
		} else if action.Send {
			select {
			case netoutgoing <- action.Entry.Message:
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

func prepareRetryAction(cache map[int]retryCacheEntry, cacheLock *sync.Mutex, key int, now time.Time, config RetryConfig) retryAction {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	entry, ok := cache[key]
	if !ok {
		return retryAction{}
	}
	if !entry.Message.Deadline.IsZero() && !now.Before(entry.Message.Deadline) {
		delete(cache, key)
		return retryAction{Entry: entry, Terminal: true, Status: DeliveryExpired, Reason: DeliveryReasonDeadline}
	}
	if entry.InFlight {
		return retryAction{}
	}
	if entry.NextRetry.IsZero() {
		entry.NextRetry = entry.Message.Cached.Add(config.AckTimeout)
	}
	if now.Before(entry.NextRetry) {
		cache[key] = entry
		return retryAction{}
	}
	if config.MaxAttempts > 0 && entry.Attempts >= config.MaxAttempts+1 {
		delete(cache, key)
		status := DeliveryDropped
		reason := DeliveryReasonMaxAttempts
		if entry.LastWriteFailed {
			status = DeliveryFailed
			reason = DeliveryReasonWriteFailure
		}
		return retryAction{Entry: entry, Terminal: true, Status: status, Reason: reason}
	}
	entry.Attempts = entry.Attempts + 1
	entry.InFlight = true
	entry.Message.Cached = now
	cache[key] = entry
	return retryAction{Entry: entry, Send: true}
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

func forwardWriteResults(ctx context.Context, config RetryConfig, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, results chan WriteResult, stats *DeliveryStats) {
	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-results:
			if !ok {
				return
			}
			handleWriteResult(config, cache, cacheLock, result, stats)
		}
	}
}

func handleWriteResult(config RetryConfig, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, result WriteResult, stats *DeliveryStats) {
	cacheLock.Lock()
	entry, exists := cache[result.Sequence]
	if !exists || entry.Message.SessionID != result.SessionID || entry.Message.MessageID != result.MessageID {
		cacheLock.Unlock()
		return
	}
	entry.InFlight = false
	entry.LastWriteFailed = result.Error != ""
	if result.Error != "" {
		entry.LastError = result.Error
	}
	now := result.Finished
	action := writeResultAction(entry, result, now, config)
	if action.Terminal {
		delete(cache, result.Sequence)
	} else {
		entry.NextRetry = now.Add(retryDelay(config, entry.Message.MessageID, entry.Attempts))
		cache[result.Sequence] = entry
	}
	cacheLock.Unlock()
	if action.Terminal {
		recordRetryTerminal(action, now, stats)
	}
}

func writeResultAction(entry retryCacheEntry, result WriteResult, now time.Time, config RetryConfig) retryAction {
	if !entry.Message.Deadline.IsZero() && !now.Before(entry.Message.Deadline) {
		return retryAction{Entry: entry, Terminal: true, Status: DeliveryExpired, Reason: DeliveryReasonDeadline}
	}
	lastAttempt := config.MaxAttempts > 0 && entry.Attempts >= config.MaxAttempts+1
	if result.Error != "" && (result.Permanent || lastAttempt) {
		return retryAction{Entry: entry, Terminal: true, Status: DeliveryFailed, Reason: DeliveryReasonWriteFailure}
	}
	return retryAction{Entry: entry}
}

func retryDelay(config RetryConfig, messageID MessageID, attempts int) time.Duration {
	base := float64(config.AckTimeout)
	maximum := float64(config.MaxRetryDelay)
	for attempt := 1; attempt < attempts && base < maximum; attempt++ {
		if base > maximum/config.BackoffMultiplier {
			base = maximum
		} else {
			base = base * config.BackoffMultiplier
		}
	}
	factor := 1.0
	if config.JitterFraction > 0 {
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", messageID, attempts)))
		randomFraction := float64(binary.BigEndian.Uint64(digest[:8])) / float64(^uint64(0))
		factor = 1 - config.JitterFraction + 2*config.JitterFraction*randomFraction
	}
	delay := base * factor
	if delay > maximum {
		delay = maximum
	}
	return time.Duration(delay)
}

func recordRetryTerminal(action retryAction, now time.Time, stats *DeliveryStats) {
	if action.Status == DeliveryExpired {
		stats.addExpired()
	} else if action.Status == DeliveryRejected {
		stats.addRejected()
	} else {
		stats.addDropped()
	}
	if action.Reason == DeliveryReasonMaxAttempts {
		logDropped(action.Entry.Message, action.Entry.Attempts)
	}
	completeEntryDelivery(action.Entry, action.Status, action.Reason, now)
}

func completeEntryDelivery(entry retryCacheEntry, status DeliveryStatus, reason DeliveryReason, now time.Time) {
	target, _ := entry.Message.Endpoint()
	result := DeliveryResult{SessionID: entry.Message.SessionID, MessageID: entry.Message.MessageID, Target: target, Status: status, Reason: reason, Attempts: entry.Attempts, Latency: now.Sub(entry.AcceptedAt), WriteError: entry.LastError}
	completeDelivery(entry.Delivery, result)
}

func finishPendingDeliveries(cache map[int]retryCacheEntry, cacheLock *sync.Mutex, now time.Time) {
	cacheLock.Lock()
	entries := make([]retryCacheEntry, 0, len(cache))
	for key, entry := range cache {
		entries = append(entries, entry)
		delete(cache, key)
	}
	cacheLock.Unlock()
	for _, entry := range entries {
		completeEntryDelivery(entry, DeliveryCanceled, DeliveryReasonServerShutdown, now)
	}
}

func handleRetryIncoming(ctx context.Context, message UdpMessage, seenCache *receivedMessageCache, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, config RetryConfig, appincoming, netoutgoing chan UdpMessage, stats *DeliveryStats, now time.Time) bool {
	if message.Type == ackType {
		entry, removed := removeAcknowledgedMessage(message, cache, cacheLock)
		if removed {
			stats.addAcked()
			completeEntryDelivery(entry, DeliveryAcknowledged, DeliveryReasonAcknowledgement, now)
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

	acknowledgement := acknowledgementForMessage(message, now)
	acknowledgement, err := applyMessageAuthentication(acknowledgement, config.AuthenticationKey)
	if err != nil {
		stats.addAuthenticationFailure()
		logAuthenticationFailure(acknowledgement, err)
		return true
	}
	select {
	case netoutgoing <- acknowledgement:
		return true
	case <-ctx.Done():
		return false
	}
}

func acknowledgementForMessage(message UdpMessage, now time.Time) UdpMessage {
	return UdpMessage{Address: message.Address, Port: message.Port, Sequence: message.Sequence, Type: ackType, Cached: now, Version: message.Version, SessionID: message.SessionID, MessageID: message.MessageID}
}

func removeAcknowledgedMessage(message UdpMessage, cache map[int]retryCacheEntry, cacheLock *sync.Mutex) (retryCacheEntry, bool) {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	entry, exists := cache[message.Sequence]
	if !exists || !acknowledgementMatchesTarget(message, entry.Message) {
		return retryCacheEntry{}, false
	}
	delete(cache, message.Sequence)
	return entry, true
}

func acknowledgementMatchesTarget(acknowledgement, outgoing UdpMessage) bool {
	if outgoing.Version == ProtocolV1 && (acknowledgement.Version != ProtocolV1 || acknowledgement.SessionID != outgoing.SessionID || acknowledgement.MessageID != outgoing.MessageID) {
		return false
	}
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

func rememberIncomingMessage(message UdpMessage, seenCache *receivedMessageCache, duplicateTTL time.Duration, now time.Time) bool {
	key := receivedMessageKey{Address: message.Address, Port: message.Port, Sequence: message.Sequence, SessionID: message.SessionID, MessageID: message.MessageID}
	return seenCache.remember(key, duplicateTTL, now)
}

// receivedMessageCache tracks recently delivered message identities. Entries
// share one TTL, so insertion order is expiry order and pruning pops expired
// queue heads instead of scanning every cached identity per inbound message.
type receivedMessageCache struct {
	entries map[receivedMessageKey]time.Time
	queue   []receivedMessageRecord
}

type receivedMessageRecord struct {
	key    receivedMessageKey
	seenAt time.Time
}

func newReceivedMessageCache() *receivedMessageCache {
	return &receivedMessageCache{entries: map[receivedMessageKey]time.Time{}}
}

func (cache *receivedMessageCache) remember(key receivedMessageKey, duplicateTTL time.Duration, now time.Time) bool {
	cache.prune(duplicateTTL, now)
	_, exists := cache.entries[key]
	if exists {
		return false
	}
	cache.entries[key] = now
	cache.queue = append(cache.queue, receivedMessageRecord{key: key, seenAt: now})
	return true
}

func (cache *receivedMessageCache) prune(duplicateTTL time.Duration, now time.Time) {
	for len(cache.queue) > 0 && now.Sub(cache.queue[0].seenAt) > duplicateTTL {
		record := cache.queue[0]
		cache.queue = cache.queue[1:]
		// Defensive: delete only the map entry this exact record created, so a
		// later re-add of the same key can never be removed by a stale record.
		if seenAt, exists := cache.entries[record.key]; exists && seenAt.Equal(record.seenAt) {
			delete(cache.entries, record.key)
		}
	}
	if len(cache.queue) == 0 {
		cache.queue = nil
	}
}

func queueOutgoingRequest(ctx context.Context, config RetryConfig, sessionID SessionID, request outboundRequest, cache map[int]retryCacheEntry, cacheLock *sync.Mutex, netoutgoing chan UdpMessage, stats *DeliveryStats) bool {
	now := time.Now()
	message, err := prepareOutgoingMessage(request.Message, config, sessionID, now)
	if err != nil {
		rejectOutgoingRequest(request, message, err, DeliveryReasonInvalidMessage, now, stats)
		return true
	}
	entry := retryCacheEntry{Message: message, AcceptedAt: now, Attempts: 1, InFlight: true, Delivery: request.Delivery}
	cacheLock.Lock()
	pending := len(cache)
	if pending >= config.MaxPending {
		cacheLock.Unlock()
		err = fmt.Errorf("queue retry UDP message %q to %s:%d failed: pending limit %d reached with %d messages", message.MessageID, message.Address, message.Port, config.MaxPending, pending)
		rejectOutgoingRequest(request, message, err, DeliveryReasonPendingLimit, now, stats)
		return true
	}
	cache[message.Sequence] = entry
	cacheLock.Unlock()
	select {
	case netoutgoing <- message:
		return true
	case <-ctx.Done():
		removeQueuedMessage(message, cache, cacheLock)
		completeEntryDelivery(entry, DeliveryCanceled, DeliveryReasonServerShutdown, time.Now())
		return false
	}
}

func prepareOutgoingMessage(message UdpMessage, config RetryConfig, sessionID SessionID, now time.Time) (UdpMessage, error) {
	message.Version = ProtocolV1
	message.SessionID = sessionID
	message.Sequence = getSequence()
	message.Type = ""
	message.Cached = now
	if message.MessageID == "" {
		messageID, err := newMessageID()
		if err != nil {
			return message, err
		}
		message.MessageID = messageID
	}
	if message.Deadline.IsZero() && config.DeliveryTimeout > 0 {
		message.Deadline = now.Add(config.DeliveryTimeout)
	} else if !message.Deadline.IsZero() && !message.Deadline.After(now) {
		return message, fmt.Errorf("queue retry UDP message %q failed: expected deadline after %v, received %v", message.MessageID, now, message.Deadline)
	}
	if message.Address == "" {
		return message, fmt.Errorf("queue retry UDP message %q failed: expected nonempty target address", message.MessageID)
	}
	authenticated, err := applyMessageAuthentication(message, config.AuthenticationKey)
	if err != nil {
		return message, err
	}
	_, err = encodeUDPMessage(authenticated)
	return authenticated, err
}

func rejectOutgoingRequest(request outboundRequest, message UdpMessage, err error, reason DeliveryReason, now time.Time, stats *DeliveryStats) {
	stats.addRejected()
	logRejectedMessage(message, err)
	entry := retryCacheEntry{Message: message, AcceptedAt: now, LastError: err.Error(), Delivery: request.Delivery}
	completeEntryDelivery(entry, DeliveryRejected, reason, now)
}

func removeQueuedMessage(message UdpMessage, cache map[int]retryCacheEntry, cacheLock *sync.Mutex) {
	cacheLock.Lock()
	defer cacheLock.Unlock()
	entry, exists := cache[message.Sequence]
	if exists && entry.Message.MessageID == message.MessageID {
		delete(cache, message.Sequence)
	}
}
