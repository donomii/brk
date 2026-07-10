package brk

import (
	"context"
	"net"
	"sync"
	"time"
)

const (
	ackType            = "Ack"
	maxPacketSize      = 32768
	packetReadSize     = maxPacketSize + 1
	maxInitialSequence = 1<<30 - 1
	// DefaultSTUNServer is the server used when STUNConfig.Server is empty.
	DefaultSTUNServer = "stun.l.google.com:19302"
)

// UdpMessage is one application message or acknowledgement on the wire.
type UdpMessage struct {
	Data     []byte
	Address  string
	Port     int
	Sequence int
	Type     string
	Cached   time.Time
}

// RetryConfig controls retry queues, timing, limits, and duplicate retention.
type RetryConfig struct {
	QueueLength   int
	RetryInterval time.Duration
	AckTimeout    time.Duration
	MaxAttempts   int
	DuplicateTTL  time.Duration
}

// STUNConfig selects a STUN server and the exchange deadline.
type STUNConfig struct {
	Server  string
	Timeout time.Duration
}

// ExternalAddress is the UDP mapping reported by a STUN server.
type ExternalAddress struct {
	Server string
	IP     string
	Port   int
	Family string
}

// DeliveryStatsSnapshot is a point-in-time copy of a server's delivery counters.
type DeliveryStatsSnapshot struct {
	Sent         uint64
	Received     uint64
	Acked        uint64
	Retried      uint64
	Duplicates   uint64
	FailedWrites uint64
	Dropped      uint64
}

// DeliveryStats stores concurrency-safe delivery counters.
type DeliveryStats struct {
	lock     sync.Mutex
	snapshot DeliveryStatsSnapshot
}

// UdpServer owns a plain UDP listener and its message queues.
type UdpServer struct {
	Incoming     chan UdpMessage
	Outgoing     chan UdpMessage
	localAddress string
	conn         *net.UDPConn
	stats        *DeliveryStats
	cancel       context.CancelFunc
	closeOnce    sync.Once
	done         chan struct{}
	closeErr     error
}

// RetryUdpServer owns a retrying message router and its plain UDP transport.
type RetryUdpServer struct {
	Incoming chan UdpMessage
	Outgoing chan UdpMessage
	Network  *UdpServer
	Config   RetryConfig
	cancel   context.CancelFunc
	done     chan struct{}
}

type retryCacheEntry struct {
	Message  UdpMessage
	Attempts int
}

type receivedMessageKey struct {
	Address  string
	Port     int
	Sequence int
}
