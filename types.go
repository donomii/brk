package brk

import (
	"context"
	"net"
	"net/netip"
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

// ProtocolVersion identifies a brk packet format.
type ProtocolVersion int

// SessionID identifies one running retry server in protocol version 1.
type SessionID string

// MessageID identifies one protocol version 1 delivery.
type MessageID string

const (
	// ProtocolLegacy is the original UdpMessage JSON packet format.
	ProtocolLegacy ProtocolVersion = 0
	// ProtocolV1 is the compact, versioned packet format with session and message IDs.
	ProtocolV1 ProtocolVersion = 1
)

// DeliveryStatus is the terminal state of a Delivery.
type DeliveryStatus string

// DeliveryReason explains why a Delivery reached its terminal state.
type DeliveryReason string

const (
	// DeliveryAcknowledged means the target returned a matching acknowledgement.
	DeliveryAcknowledged DeliveryStatus = "acknowledged"
	// DeliveryDropped means the configured attempt limit was reached.
	DeliveryDropped DeliveryStatus = "dropped"
	// DeliveryFailed means the final or permanent UDP write failed.
	DeliveryFailed DeliveryStatus = "failed"
	// DeliveryExpired means the delivery deadline elapsed.
	DeliveryExpired DeliveryStatus = "expired"
	// DeliveryRejected means validation or pending capacity prevented queueing.
	DeliveryRejected DeliveryStatus = "rejected"
	// DeliveryCanceled means server shutdown ended a pending delivery.
	DeliveryCanceled DeliveryStatus = "canceled"

	// DeliveryReasonAcknowledgement identifies a matching acknowledgement.
	DeliveryReasonAcknowledgement DeliveryReason = "acknowledgement"
	// DeliveryReasonDeadline identifies an elapsed delivery deadline.
	DeliveryReasonDeadline DeliveryReason = "deadline"
	// DeliveryReasonMaxAttempts identifies an exhausted attempt limit.
	DeliveryReasonMaxAttempts DeliveryReason = "max_attempts"
	// DeliveryReasonWriteFailure identifies a terminal UDP write error.
	DeliveryReasonWriteFailure DeliveryReason = "write_failure"
	// DeliveryReasonPendingLimit identifies a full pending-delivery queue.
	DeliveryReasonPendingLimit DeliveryReason = "pending_limit"
	// DeliveryReasonInvalidMessage identifies a message rejected before queueing.
	DeliveryReasonInvalidMessage DeliveryReason = "invalid_message"
	// DeliveryReasonServerShutdown identifies a server closed before acknowledgement.
	DeliveryReasonServerShutdown DeliveryReason = "server_shutdown"
)

// UdpMessage is one application message or acknowledgement on the wire.
type UdpMessage struct {
	Data      []byte
	Address   string
	Port      int
	Sequence  int
	Type      string
	Cached    time.Time
	Version   ProtocolVersion
	SessionID SessionID
	MessageID MessageID
	Deadline  time.Time
	Signature string
}

// RetryConfig controls retry queues, timing, limits, and duplicate retention.
type RetryConfig struct {
	// QueueLength is the channel capacity for incoming and outgoing messages.
	QueueLength int
	// RetryInterval is how often the pending queue is checked.
	RetryInterval time.Duration
	// AckTimeout is the base delay between a completed write and its first retry.
	AckTimeout time.Duration
	// MaxAttempts limits retransmissions after the initial write; zero removes the attempt limit.
	MaxAttempts int
	// DuplicateTTL is how long received message identities remain suppressed.
	DuplicateTTL time.Duration
	// MaxPending limits messages waiting for a terminal result.
	MaxPending int
	// BackoffMultiplier grows the delay after each completed attempt.
	BackoffMultiplier float64
	// MaxRetryDelay caps exponential backoff.
	MaxRetryDelay time.Duration
	// JitterFraction varies retry delay deterministically in [0, 1).
	JitterFraction float64
	// DisableJitter requests exact exponential delays.
	DisableJitter bool
	// DeliveryTimeout supplies a deadline to messages without an explicit deadline.
	DeliveryTimeout time.Duration
	// DisableDeliveryTimeout removes the default delivery deadline.
	DisableDeliveryTimeout bool
	// AuthenticationKey optionally signs version 1 packets with HMAC-SHA256 and must contain at least 32 bytes.
	AuthenticationKey []byte
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
	Sent                   uint64
	Received               uint64
	Acked                  uint64
	Retried                uint64
	Duplicates             uint64
	FailedWrites           uint64
	Dropped                uint64
	Expired                uint64
	Rejected               uint64
	AuthenticationFailures uint64
	InvalidPackets         uint64
}

// SendRequest describes one receipt-tracked retry message.
type SendRequest struct {
	// Data is copied before the request is queued.
	Data []byte
	// Target is a valid peer endpoint in the server's address family.
	Target netip.AddrPort
	// Deadline is an optional absolute terminal deadline; zero uses RetryConfig.DeliveryTimeout.
	Deadline time.Time
}

// DeliveryResult describes the one terminal result produced by a Delivery.
type DeliveryResult struct {
	// SessionID and MessageID identify the delivery.
	SessionID SessionID
	MessageID MessageID
	// Target is the normalized destination endpoint.
	Target netip.AddrPort
	// Status and Reason describe the terminal outcome.
	Status DeliveryStatus
	Reason DeliveryReason
	// Attempts counts dispatched writes, including the initial write.
	Attempts int
	// Latency is the elapsed time from acceptance to the terminal outcome.
	Latency time.Duration
	// WriteError contains the latest exact UDP write error, if any.
	WriteError string
}

// Delivery exposes the eventual terminal result of one retry message.
type Delivery struct {
	messageID    MessageID
	done         chan struct{}
	lock         sync.Mutex
	result       DeliveryResult
	completeOnce sync.Once
}

// WriteResult describes one completed application-message UDP write.
type WriteResult struct {
	// SessionID, MessageID, and Sequence identify the write.
	SessionID SessionID
	MessageID MessageID
	Sequence  int
	// Target is the normalized endpoint resolved by the writer.
	Target netip.AddrPort
	// Finished is when the write attempt completed.
	Finished time.Time
	// Error contains the exact write error, or is empty after a successful write.
	Error string
	// Permanent reports whether retrying the unchanged message cannot succeed.
	Permanent bool
}

// PunchConfig controls active peer-punch attempts.
type PunchConfig struct {
	// Attempts is the maximum number of binding requests to send.
	Attempts int
	// Interval is the response deadline for each attempt.
	Interval time.Duration
}

// PunchResult describes a successful peer punch.
type PunchResult struct {
	// Peer is the contacted peer endpoint.
	Peer netip.AddrPort
	// ObservedAddress is this server's endpoint as reported by the peer.
	ObservedAddress netip.AddrPort
	// Attempts is the successful attempt number.
	Attempts int
	// RoundTrip is the elapsed time for the successful exchange.
	RoundTrip time.Duration
}

// KeepaliveConfig controls the interval between peer mapping keepalives.
type KeepaliveConfig struct {
	// Interval is the time between STUN binding indications.
	Interval time.Duration
}

// DeliveryStats stores concurrency-safe delivery counters.
type DeliveryStats struct {
	lock     sync.Mutex
	snapshot DeliveryStatsSnapshot
}

// UdpServer owns a plain UDP listener and its message queues.
type UdpServer struct {
	Incoming        chan UdpMessage
	Outgoing        chan UdpMessage
	ctx             context.Context
	localAddress    string
	localEndpoint   netip.AddrPort
	conn            *net.UDPConn
	stats           *DeliveryStats
	writeResults    chan WriteResult
	controlOutgoing chan outboundDatagram
	stunLock        sync.Mutex
	stunResponses   chan receivedDatagram
	cancel          context.CancelFunc
	closeOnce       sync.Once
	done            chan struct{}
	closeErr        error
}

// RetryUdpServer owns a retrying message router and its plain UDP transport.
type RetryUdpServer struct {
	Incoming  chan UdpMessage
	Outgoing  chan UdpMessage
	Network   *UdpServer
	Config    RetryConfig
	requests  chan outboundRequest
	sessionID SessionID
	cancel    context.CancelFunc
	done      chan struct{}
}

type retryCacheEntry struct {
	Message         UdpMessage
	AcceptedAt      time.Time
	NextRetry       time.Time
	Attempts        int
	InFlight        bool
	LastError       string
	LastWriteFailed bool
	Delivery        *Delivery
}

type retryAction struct {
	Entry    retryCacheEntry
	Send     bool
	Terminal bool
	Status   DeliveryStatus
	Reason   DeliveryReason
}

type receivedMessageKey struct {
	Address   string
	Port      int
	Sequence  int
	SessionID SessionID
	MessageID MessageID
}

type outboundRequest struct {
	Message  UdpMessage
	Delivery *Delivery
}

type outboundDatagram struct {
	Packet      []byte
	Destination netip.AddrPort
	Result      chan error
	Operation   string
}

type receivedDatagram struct {
	Packet []byte
	Source netip.AddrPort
}
