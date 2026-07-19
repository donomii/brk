package brk

import (
	"sort"
	"time"
)

// Ordered delivery holds inbound application messages back per peer session
// until every earlier sequence has been delivered, so the application reads
// them in the sender's order. Sequences come from one process-wide counter, so
// a gap in one stream can belong to traffic for another target and may never
// fill; a held message therefore waits at most OrderingHoldTimeout before its
// stream releases it and moves on. A message arriving after its slot was
// passed is delivered immediately rather than dropped.

// orderingStreamKey identifies one sending session at one peer endpoint.
type orderingStreamKey struct {
	Address   string
	Port      int
	SessionID SessionID
}

type heldMessage struct {
	message UdpMessage
	// spanStart is the lowest sequence the message covers: its own sequence,
	// or the lowest fragment sequence of a reassembled message.
	spanStart int
	heldAt    time.Time
}

type orderingStream struct {
	started          bool
	highestDelivered int
	held             []heldMessage // sorted by spanStart
	lastActivity     time.Time
}

type orderingCache struct {
	streams map[orderingStreamKey]*orderingStream
}

func newOrderingCache() *orderingCache {
	return &orderingCache{streams: map[orderingStreamKey]*orderingStream{}}
}

// add accepts one deliverable message covering sequences [spanStart,
// message.Sequence] and returns the messages releasable now, in delivery
// order. The first message of a stream establishes its delivery floor.
func (cache *orderingCache) add(message UdpMessage, spanStart int, now time.Time) []UdpMessage {
	key := orderingStreamKey{Address: message.Address, Port: message.Port, SessionID: message.SessionID}
	stream, exists := cache.streams[key]
	if !exists {
		stream = &orderingStream{}
		cache.streams[key] = stream
	}
	stream.lastActivity = now
	if stream.started && spanStart > stream.highestDelivered+1 {
		stream.hold(message, spanStart, now)
		return nil
	}
	if !stream.started || message.Sequence > stream.highestDelivered {
		stream.highestDelivered = message.Sequence
	}
	stream.started = true
	released := []UdpMessage{message}
	return append(released, stream.drainContiguous()...)
}

// flush releases every held message whose gap wait reached holdTimeout plus
// any messages its release unblocks, and forgets streams idle beyond idleTTL.
func (cache *orderingCache) flush(holdTimeout, idleTTL time.Duration, now time.Time) []UdpMessage {
	var released []UdpMessage
	for key, stream := range cache.streams {
		for len(stream.held) > 0 && (stream.held[0].spanStart <= stream.highestDelivered+1 || now.Sub(stream.held[0].heldAt) >= holdTimeout) {
			released = append(released, stream.releaseHead())
		}
		if len(stream.held) == 0 && now.Sub(stream.lastActivity) > idleTTL {
			delete(cache.streams, key)
		}
	}
	return released
}

func (stream *orderingStream) hold(message UdpMessage, spanStart int, now time.Time) {
	at := sort.Search(len(stream.held), func(index int) bool { return stream.held[index].spanStart > spanStart })
	stream.held = append(stream.held, heldMessage{})
	copy(stream.held[at+1:], stream.held[at:])
	stream.held[at] = heldMessage{message: message, spanStart: spanStart, heldAt: now}
}

func (stream *orderingStream) drainContiguous() []UdpMessage {
	var released []UdpMessage
	for len(stream.held) > 0 && stream.held[0].spanStart <= stream.highestDelivered+1 {
		released = append(released, stream.releaseHead())
	}
	return released
}

func (stream *orderingStream) releaseHead() UdpMessage {
	head := stream.held[0]
	stream.held = stream.held[1:]
	if len(stream.held) == 0 {
		stream.held = nil
	}
	if head.message.Sequence > stream.highestDelivered {
		stream.highestDelivered = head.message.Sequence
	}
	return head.message
}

// orderingFlushInterval is how often held messages are checked for gap
// timeout, bounding how late a release can fire after its deadline.
func orderingFlushInterval(holdTimeout time.Duration) time.Duration {
	interval := holdTimeout / 4
	if interval <= 0 {
		return holdTimeout
	}
	return interval
}
