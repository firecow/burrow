package ling

import (
	"context"
	"net"

	"github.com/firecow/burrow/internal/state"
)

// TestUpdateTargets exposes updateTargets for external tests.
func (proxy *TCPProxy) TestUpdateTargets(targets []ProxyTarget) {
	proxy.updateTargets(targets)
}

// TestTrackUpstream exposes trackUpstream for external tests.
func (proxy *TCPProxy) TestTrackUpstream(addr string, conn net.Conn) {
	proxy.trackUpstream(addr, conn)
}

// TestUntrackUpstream exposes untrackUpstream for external tests.
func (proxy *TCPProxy) TestUntrackUpstream(addr string, conn net.Conn) {
	proxy.untrackUpstream(addr, conn)
}

// TestStart exposes start for external tests.
func (proxy *TCPProxy) TestStart(ctx context.Context) error {
	return proxy.start(ctx)
}

// TestClose exposes close for external tests.
func (proxy *TCPProxy) TestClose(ctx context.Context) {
	proxy.close(ctx)
}

// TestListenerAddr returns the address the proxy listens on.
func (proxy *TCPProxy) TestListenerAddr() string {
	return proxy.listener.Addr().String()
}

// TestUpstreamCount returns the number of tracked upstreams for a given address.
func (proxy *TCPProxy) TestUpstreamCount(addr string) int {
	proxy.upstreamMu.Lock()
	defer proxy.upstreamMu.Unlock()

	return len(proxy.upstreams[addr])
}

// NewProxyTarget creates a ProxyTarget for external tests.
func NewProxyTarget(host string, remotePort int) ProxyTarget {
	return ProxyTarget{host: host, remotePort: remotePort}
}

// SyncTunnelServiceIDs exposes buildSyncTunnels and returns the reported service IDs per tunnel name.
func SyncTunnelServiceIDs(
	tunnelConfigs []TunnelConfig,
	stateSnapshot *state.State,
	lingID string,
) map[string]string {
	reported := make(map[string]string)

	for _, tunnel := range buildSyncTunnels(tunnelConfigs, stateSnapshot, lingID) {
		reported[tunnel.Name] = tunnel.ServiceID
	}

	return reported
}
