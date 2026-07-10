package brk

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"
)

// PunchPeer sends marked STUN binding requests from the live server socket until the peer responds.
func (server *UdpServer) PunchPeer(ctx context.Context, peer netip.AddrPort, config PunchConfig) (PunchResult, error) {
	if ctx == nil {
		return PunchResult{}, fmt.Errorf("punch UDP peer failed: expected non-nil context")
	}
	peer, err := validatePeerFamily(peer, server.LocalEndpoint(), "punch UDP peer")
	if err != nil {
		return PunchResult{}, err
	}
	resolved, err := ResolvePunchConfig(config)
	if err != nil {
		return PunchResult{}, err
	}
	started := time.Now()
	var lastErr error
	for attempt := 1; attempt <= resolved.Attempts; attempt++ {
		result, exchangeErr := server.punchAttempt(ctx, peer, resolved.Interval, attempt)
		if exchangeErr == nil {
			return result, nil
		}
		lastErr = exchangeErr
		if ctx.Err() != nil || serverClosed(server.Done()) {
			break
		}
	}
	return PunchResult{}, fmt.Errorf("punch UDP peer %v failed after %d attempts at %v intervals over %v: %w", peer, resolved.Attempts, resolved.Interval, time.Since(started), lastErr)
}

// PunchPeer sends marked STUN binding requests from the retry server's live socket until the peer responds.
func (server *RetryUdpServer) PunchPeer(ctx context.Context, peer netip.AddrPort, config PunchConfig) (PunchResult, error) {
	return server.Network.PunchPeer(ctx, peer, config)
}

func (server *UdpServer) punchAttempt(ctx context.Context, peer netip.AddrPort, interval time.Duration, attempt int) (PunchResult, error) {
	transactionID, err := newSTUNTransactionID()
	if err != nil {
		return PunchResult{}, err
	}
	started := time.Now()
	response, err := server.exchangeSTUN(ctx, peer, makeSTUNPeerRequest(transactionID), transactionID, interval, "send peer STUN binding request")
	if err != nil {
		return PunchResult{}, err
	}
	address, err := parseSTUNBindingResponse(response.Packet, transactionID, peer.String())
	if err != nil {
		return PunchResult{}, err
	}
	observed, err := address.Endpoint()
	if err != nil {
		return PunchResult{}, err
	}
	return PunchResult{Peer: peer, ObservedAddress: observed, Attempts: attempt, RoundTrip: time.Since(started)}, nil
}

// KeepPeerAlive sends immediate and periodic binding indications from the live socket until ctx or the server ends.
func (server *UdpServer) KeepPeerAlive(ctx context.Context, peer netip.AddrPort, config KeepaliveConfig) error {
	if ctx == nil {
		return fmt.Errorf("keep UDP peer alive failed: expected non-nil context")
	}
	peer, err := validatePeerFamily(peer, server.LocalEndpoint(), "keep UDP peer alive")
	if err != nil {
		return err
	}
	resolved, err := ResolveKeepaliveConfig(config)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(resolved.Interval)
	defer ticker.Stop()
	for {
		err = server.sendKeepalive(ctx, peer)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || serverClosed(server.Done()) {
				return nil
			}
			return err
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return nil
		case <-server.Done():
			return nil
		}
	}
}

// KeepPeerAlive sends binding indications from the retry server's live socket until ctx or the server ends.
func (server *RetryUdpServer) KeepPeerAlive(ctx context.Context, peer netip.AddrPort, config KeepaliveConfig) error {
	return server.Network.KeepPeerAlive(ctx, peer, config)
}

func (server *UdpServer) sendKeepalive(ctx context.Context, peer netip.AddrPort) error {
	transactionID, err := newSTUNTransactionID()
	if err != nil {
		return err
	}
	err = server.sendControlDatagram(ctx, makeSTUNBindingIndication(transactionID), peer, "send STUN keepalive indication")
	if err != nil {
		return fmt.Errorf("keep UDP peer %v alive from %v failed: %w", peer, server.LocalEndpoint(), err)
	}
	return nil
}

func validatePeerFamily(peer, local netip.AddrPort, operation string) (netip.AddrPort, error) {
	peer, err := validateEndpoint(peer, operation)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if peer.Addr().Is4() != local.Addr().Is4() {
		return netip.AddrPort{}, fmt.Errorf("%s failed: peer family %s does not match local endpoint %v", operation, addressFamily(peer.Addr()), local)
	}
	return peer, nil
}

func serverClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
