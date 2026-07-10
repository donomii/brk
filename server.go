package brk

import (
	"errors"
	"fmt"
	"net"
)

// Close stops a plain UDP server and waits for its network I/O loops to exit.
func (server *UdpServer) Close() error {
	server.closeOnce.Do(func() {
		server.cancel()
		err := server.conn.Close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			server.closeErr = fmt.Errorf("close UDP server on %s: %w", server.localAddress, err)
		}
		<-server.done
	})
	return server.closeErr
}

// Done closes after the plain UDP server's network I/O loops exit.
func (server *UdpServer) Done() <-chan struct{} {
	return server.done
}

// LocalAddress returns the bound local UDP address.
func (server *UdpServer) LocalAddress() string {
	return server.localAddress
}

// Stats returns a point-in-time copy of the delivery counters.
func (server *UdpServer) Stats() DeliveryStatsSnapshot {
	return server.stats.Snapshot()
}

// WriteResults returns completed application-message write attempts until the server stops.
func (server *UdpServer) WriteResults() <-chan WriteResult {
	return server.writeResults
}

// Close stops a retry server and waits for its retry and network I/O loops to exit.
func (server *RetryUdpServer) Close() error {
	server.cancel()
	err := server.Network.Close()
	<-server.done
	return err
}

// Done closes after the retry server's retry and network I/O loops exit.
func (server *RetryUdpServer) Done() <-chan struct{} {
	return server.done
}

// LocalAddress returns the retry server's bound local UDP address.
func (server *RetryUdpServer) LocalAddress() string {
	return server.Network.LocalAddress()
}

// Stats returns a point-in-time copy of the retry server's delivery counters.
func (server *RetryUdpServer) Stats() DeliveryStatsSnapshot {
	return server.Network.Stats()
}
