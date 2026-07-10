package brk

// NewDeliveryStats returns zeroed delivery counters.
func NewDeliveryStats() *DeliveryStats {
	return &DeliveryStats{}
}

// Snapshot returns a concurrency-safe copy of the delivery counters.
func (stats *DeliveryStats) Snapshot() DeliveryStatsSnapshot {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	return stats.snapshot
}

func (stats *DeliveryStats) addSent() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Sent = stats.snapshot.Sent + 1
}

func (stats *DeliveryStats) addReceived() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Received = stats.snapshot.Received + 1
}

func (stats *DeliveryStats) addAcked() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Acked = stats.snapshot.Acked + 1
}

func (stats *DeliveryStats) addRetried() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Retried = stats.snapshot.Retried + 1
}

func (stats *DeliveryStats) addDuplicate() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Duplicates = stats.snapshot.Duplicates + 1
}

func (stats *DeliveryStats) addFailedWrite() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.FailedWrites = stats.snapshot.FailedWrites + 1
}

func (stats *DeliveryStats) addDropped() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Dropped = stats.snapshot.Dropped + 1
}

func (stats *DeliveryStats) addExpired() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Expired = stats.snapshot.Expired + 1
}

func (stats *DeliveryStats) addRejected() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.Rejected = stats.snapshot.Rejected + 1
}

func (stats *DeliveryStats) addAuthenticationFailure() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.AuthenticationFailures = stats.snapshot.AuthenticationFailures + 1
}

func (stats *DeliveryStats) addInvalidPacket() {
	stats.lock.Lock()
	defer stats.lock.Unlock()
	stats.snapshot.InvalidPackets = stats.snapshot.InvalidPackets + 1
}
