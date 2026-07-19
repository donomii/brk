package brk

import (
	"fmt"
	"sync"
	"time"
)

const (
	// fragmentDatagramBudget targets a fragment's encoded datagram size at a
	// conservative path MTU, so each fragment is a single IP packet and stays
	// well under the per-datagram limits some platforms impose (macOS defaults
	// net.inet.udp.maxdgram to 9216 bytes).
	fragmentDatagramBudget = 1400
	// jsonFragmentOverhead bounds the version 1 JSON envelope around the
	// base64 payload of a fragment.
	jsonFragmentOverhead = 240
	// binaryFragmentOverhead is the version 2 fragment header plus signature
	// trailer.
	binaryFragmentOverhead = binaryFragmentHeaderSize + binarySignatureSize
)

// defaultFragmentPayload is the MTU-safe payload one fragment carries by
// default. base64 inflates version 1 payloads by 4/3, so its budget is scaled
// down before the divide.
func defaultFragmentPayload(version ProtocolVersion) int {
	if version == ProtocolV2 {
		return fragmentDatagramBudget - binaryFragmentOverhead
	}
	return (fragmentDatagramBudget - jsonFragmentOverhead) * 3 / 4
}

// maxFragmentPayload is the largest payload a fragment can physically carry
// while its encoded packet still fits maxPacketSize. It bounds an explicit
// FragmentPayloadBytes override; the default stays MTU-safe.
func maxFragmentPayload(version ProtocolVersion) int {
	if version == ProtocolV2 {
		return maxPacketSize - binaryFragmentOverhead
	}
	return (maxPacketSize - jsonFragmentOverhead) * 3 / 4
}

// fragmentPayloadLimit returns the application bytes one packet may carry
// before an outgoing message is split into fragments.
func fragmentPayloadLimit(config RetryConfig) int {
	if config.FragmentPayloadBytes > 0 {
		return config.FragmentPayloadBytes
	}
	return defaultFragmentPayload(config.WireVersion)
}

func fragmentOutgoingMessage(message UdpMessage, limit int) ([]UdpMessage, error) {
	group, err := newWireID("fragment group")
	if err != nil {
		return nil, err
	}
	count := (len(message.Data) + limit - 1) / limit
	fragments := make([]UdpMessage, 0, count)
	for index := 0; index < count; index++ {
		start := index * limit
		end := min(start+limit, len(message.Data))
		fragment := message
		fragment.Data = append([]byte(nil), message.Data[start:end]...)
		fragment.MessageID = ""
		fragment.FragmentGroup = group
		fragment.FragmentIndex = index
		fragment.FragmentCount = count
		fragments = append(fragments, fragment)
	}
	return fragments, nil
}

// fragmentGroupDelivery folds per-fragment terminal results into the one
// delivery handle the caller holds: acknowledged when every fragment is
// acknowledged, otherwise the first failure wins and later fragment results
// are ignored.
type fragmentGroupDelivery struct {
	lock       sync.Mutex
	delivery   *Delivery
	remaining  int
	attempts   int
	done       bool
	acceptedAt time.Time
}

func newFragmentGroupDelivery(delivery *Delivery, count int, acceptedAt time.Time) *fragmentGroupDelivery {
	return &fragmentGroupDelivery{delivery: delivery, remaining: count, acceptedAt: acceptedAt}
}

func (group *fragmentGroupDelivery) complete(entry retryCacheEntry, status DeliveryStatus, reason DeliveryReason, now time.Time) {
	group.lock.Lock()
	if group.done {
		group.lock.Unlock()
		return
	}
	group.attempts = group.attempts + entry.Attempts
	if status == DeliveryAcknowledged && group.remaining > 1 {
		group.remaining = group.remaining - 1
		group.lock.Unlock()
		return
	}
	group.done = true
	attempts := group.attempts
	group.lock.Unlock()

	target, _ := entry.Message.Endpoint()
	result := DeliveryResult{SessionID: entry.Message.SessionID, MessageID: MessageID(entry.Message.FragmentGroup), Target: target, Status: status, Reason: reason, Attempts: attempts, Latency: now.Sub(group.acceptedAt), WriteError: entry.LastError}
	completeDelivery(group.delivery, result)
}

// reassemblyKey identifies one fragment group from one peer.
type reassemblyKey struct {
	Address   string
	Port      int
	SessionID SessionID
	Group     string
}

type reassemblyRecord struct {
	key       reassemblyKey
	startedAt time.Time
	created   uint64
}

type reassemblyGroup struct {
	count       int
	parts       map[int][]byte
	startedAt   time.Time
	created     uint64
	bytes       int
	minSequence int
	maxSequence int
}

// reassemblyCache collects fragments until a group completes. Groups share
// one TTL, so insertion order is expiry order, mirroring receivedMessageCache.
type reassemblyCache struct {
	groups    map[reassemblyKey]*reassemblyGroup
	queue     []reassemblyRecord
	bytes     int
	created   uint64
	maxGroups int
	maxBytes  int
	stats     *DeliveryStats
}

func newReassemblyCache() *reassemblyCache {
	return newReassemblyCacheWithLimits(defaultMaxReassemblyGroups, defaultMaxReassemblyBytes, nil)
}

func newReassemblyCacheWithLimits(maxGroups, maxBytes int, stats *DeliveryStats) *reassemblyCache {
	return &reassemblyCache{groups: map[reassemblyKey]*reassemblyGroup{}, maxGroups: maxGroups, maxBytes: maxBytes, stats: stats}
}

// add stores one fragment and returns the assembled message once its group is
// complete. The assembled message carries the fragment group ID as its
// message ID, matching the sender's delivery result, and the group's highest
// fragment sequence; the returned span start is the lowest, so ordered
// delivery can treat the assembled message as covering the group's whole
// sequence range.
func (cache *reassemblyCache) add(message UdpMessage, reassemblyTTL time.Duration, now time.Time) (UdpMessage, int, bool, error) {
	cache.prune(reassemblyTTL, now)
	key := reassemblyKey{Address: message.Address, Port: message.Port, SessionID: message.SessionID, Group: message.FragmentGroup}
	group, exists := cache.groups[key]
	if exists && group.count != message.FragmentCount {
		err := fmt.Errorf("reassemble fragment group %q from %s:%d: expected %d fragments, received a fragment claiming %d", message.FragmentGroup, message.Address, message.Port, group.count, message.FragmentCount)
		cache.recordReassemblyRejection()
		return UdpMessage{}, 0, false, err
	}
	if len(message.Data) > cache.maxBytes {
		err := fmt.Errorf("reassemble fragment group %q from %s:%d rejected: fragment payload is %d bytes, max retained reassembly bytes is %d", message.FragmentGroup, message.Address, message.Port, len(message.Data), cache.maxBytes)
		cache.recordReassemblyRejection()
		return UdpMessage{}, 0, false, err
	}

	evicted := 0
	if !exists {
		for len(cache.groups) >= cache.maxGroups {
			if !cache.evictOldest(reassemblyKey{}, false) {
				break
			}
			evicted++
		}
		cache.created++
		group = &reassemblyGroup{count: message.FragmentCount, parts: map[int][]byte{}, startedAt: now, created: cache.created, minSequence: message.Sequence, maxSequence: message.Sequence}
		cache.groups[key] = group
		cache.queue = append(cache.queue, reassemblyRecord{key: key, startedAt: now, created: group.created})
	}

	previous := group.parts[message.FragmentIndex]
	additionalBytes := len(message.Data) - len(previous)
	for additionalBytes > 0 && cache.bytes+additionalBytes > cache.maxBytes {
		if !cache.evictOldest(key, true) {
			err := fmt.Errorf("reassemble fragment group %q from %s:%d rejected: adding %d payload bytes would retain %d bytes, max is %d and no other incomplete group remains to evict", message.FragmentGroup, message.Address, message.Port, additionalBytes, cache.bytes+additionalBytes, cache.maxBytes)
			cache.recordReassemblyRejection()
			cache.recordReassemblyEvictions(message, evicted)
			return UdpMessage{}, 0, false, err
		}
		evicted++
	}
	cache.recordReassemblyEvictions(message, evicted)

	group.parts[message.FragmentIndex] = message.Data
	group.bytes = group.bytes + additionalBytes
	cache.bytes = cache.bytes + additionalBytes
	if message.Sequence < group.minSequence {
		group.minSequence = message.Sequence
	}
	if message.Sequence > group.maxSequence {
		group.maxSequence = message.Sequence
	}
	if len(group.parts) < group.count {
		return UdpMessage{}, 0, false, nil
	}

	total := 0
	for _, part := range group.parts {
		total = total + len(part)
	}
	data := make([]byte, 0, total)
	for index := 0; index < group.count; index++ {
		data = append(data, group.parts[index]...)
	}
	assembled := message
	assembled.Data = data
	assembled.Sequence = group.maxSequence
	assembled.MessageID = MessageID(message.FragmentGroup)
	assembled.FragmentGroup = ""
	assembled.FragmentIndex = 0
	assembled.FragmentCount = 0
	cache.dropGroup(key)
	cache.compactQueue()
	return assembled, group.minSequence, true, nil
}

func (cache *reassemblyCache) prune(reassemblyTTL time.Duration, now time.Time) {
	for len(cache.queue) > 0 {
		record := cache.queue[0]
		group, exists := cache.groups[record.key]
		if exists && group.created == record.created && now.Sub(record.startedAt) <= reassemblyTTL {
			break
		}
		cache.queue = cache.queue[1:]
		if exists && group.created == record.created {
			cache.dropGroup(record.key)
		}
	}
	if len(cache.queue) == 0 {
		cache.queue = nil
	}
}

func (cache *reassemblyCache) evictOldest(excluded reassemblyKey, hasExcluded bool) bool {
	for _, record := range cache.queue {
		group, exists := cache.groups[record.key]
		if !exists || group.created != record.created || hasExcluded && record.key == excluded {
			continue
		}
		cache.dropGroup(record.key)
		cache.compactQueue()
		return true
	}
	return false
}

func (cache *reassemblyCache) dropGroup(key reassemblyKey) {
	group, exists := cache.groups[key]
	if !exists {
		return
	}
	cache.bytes = cache.bytes - group.bytes
	delete(cache.groups, key)
}

func (cache *reassemblyCache) compactQueue() {
	if len(cache.queue) <= cache.maxGroups*2 {
		return
	}
	active := cache.queue[:0]
	for _, record := range cache.queue {
		group, exists := cache.groups[record.key]
		if exists && group.created == record.created {
			active = append(active, record)
		}
	}
	cache.queue = active
}

func (cache *reassemblyCache) recordReassemblyEvictions(message UdpMessage, count int) {
	if count == 0 {
		return
	}
	if cache.stats != nil {
		cache.stats.addReassemblyEvictions(count)
	}
	logReassemblyEvictions(message, count, len(cache.groups), cache.bytes)
}

func (cache *reassemblyCache) recordReassemblyRejection() {
	if cache.stats != nil {
		cache.stats.addReassemblyRejection()
	}
}
