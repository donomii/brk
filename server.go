package brk

import (
	"errors"
	"fmt"
	"net"
)

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

func (server *UdpServer) Done() <-chan struct{} {
	return server.done
}

func (server *UdpServer) LocalAddress() string {
	return server.localAddress
}

func (server *UdpServer) Stats() DeliveryStatsSnapshot {
	return server.stats.Snapshot()
}

func (server *RetryUdpServer) Close() error {
	server.cancel()
	return server.Network.Close()
}

func (server *RetryUdpServer) Done() <-chan struct{} {
	return server.Network.Done()
}

func (server *RetryUdpServer) LocalAddress() string {
	return server.Network.LocalAddress()
}

func (server *RetryUdpServer) Stats() DeliveryStatsSnapshot {
	return server.Network.Stats()
}
