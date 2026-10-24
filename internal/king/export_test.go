package king

import (
	"context"

	"github.com/quic-go/quic-go"
)

// SetQUICConn sets a QUIC connection for a service ID (test helper).
func (tunnelSrv *TunnelServer) SetQUICConn(serviceID string, conn *quic.Conn) {
	tunnelSrv.mu.Lock()
	defer tunnelSrv.mu.Unlock()

	tunnelSrv.quicConns[serviceID] = conn
}

// GetServiceAuth returns the service auth and whether it exists (test helper).
func (tunnelSrv *TunnelServer) GetServiceAuth(serviceID string) (ServiceAuth, bool) {
	tunnelSrv.mu.RLock()
	defer tunnelSrv.mu.RUnlock()

	auth, exists := tunnelSrv.services[serviceID]

	return auth, exists
}

// ServiceCount returns the number of registered services (test helper).
func (tunnelSrv *TunnelServer) ServiceCount() int {
	tunnelSrv.mu.RLock()
	defer tunnelSrv.mu.RUnlock()

	return len(tunnelSrv.services)
}

// SetServiceAuth sets a service auth entry (test helper).
func (tunnelSrv *TunnelServer) SetServiceAuth(serviceID string, auth ServiceAuth) {
	tunnelSrv.mu.Lock()
	defer tunnelSrv.mu.Unlock()

	tunnelSrv.services[serviceID] = auth
}

// RemoveQUICConn removes a QUIC connection and signals drainNotify (test helper).
func (tunnelSrv *TunnelServer) RemoveQUICConn(serviceID string) {
	tunnelSrv.mu.Lock()

	delete(tunnelSrv.quicConns, serviceID)

	empty := len(tunnelSrv.quicConns) == 0
	tunnelSrv.mu.Unlock()

	if empty {
		select {
		case tunnelSrv.drainNotify <- struct{}{}:
		default:
		}
	}
}

// QUICConnCount returns the number of QUIC connections (test helper).
func (tunnelSrv *TunnelServer) QUICConnCount() int {
	tunnelSrv.mu.RLock()
	defer tunnelSrv.mu.RUnlock()

	return len(tunnelSrv.quicConns)
}

// WaitForDrain exposes the private waitForDrain method (test helper).
func (tunnelSrv *TunnelServer) WaitForDrain(ctx context.Context) {
	tunnelSrv.waitForDrain(ctx)
}

// TCPServiceID returns the service ID a TCP listener port routes to (test helper).
func (tunnelSrv *TunnelServer) TCPServiceID(remotePort int) string {
	tunnelSrv.mu.RLock()
	defer tunnelSrv.mu.RUnlock()

	return tunnelSrv.tcpServiceIDs[remotePort]
}
