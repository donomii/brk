package brk

import (
	"context"
	"net"
	"sync"
	"time"
)

const (
	ackType           = "Ack"
	maxPacketSize     = 32768
	DefaultSTUNServer = "stun.l.google.com:19302"
)

type UdpMessage struct {
	Data     []byte
	Address  string
	Port     int
	Sequence int
	Type     string
	Cached   time.Time
}

type RetryConfig struct {
	QueueLength   int
	RetryInterval time.Duration
	AckTimeout    time.Duration
	MaxAttempts   int
	DuplicateTTL  time.Duration
}

type STUNConfig struct {
	Server  string
	Timeout time.Duration
}

type ExternalAddress struct {
	Server string
	IP     string
	Port   int
	Family string
}

type DeliveryStatsSnapshot struct {
	Sent         uint64
	Received     uint64
	Acked        uint64
	Retried      uint64
	Duplicates   uint64
	FailedWrites uint64
	Dropped      uint64
}

type DeliveryStats struct {
	lock     sync.Mutex
	snapshot DeliveryStatsSnapshot
}

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

type RetryUdpServer struct {
	Incoming chan UdpMessage
	Outgoing chan UdpMessage
	Network  *UdpServer
	Config   RetryConfig
	cancel   context.CancelFunc
}

type retryCacheEntry struct {
	Message  UdpMessage
	Attempts int
}
