package king

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

const (
	quicKeepAlivePeriod    = 2 * time.Second
	quicMaxIdleTimeout     = 4 * time.Second
	quicMaxIncomingStreams = 1024
	quicAuthFailCode       = 2
	quicStreamOpenTimeout  = 2 * time.Second
	quicStreamOpenRetries  = 3
	quicStreamRetryDelay   = 500 * time.Millisecond
	serviceIDHeaderBytes   = 2
)

var errServiceIDTooLong = errors.New("service ID too long")

// ControlMessage represents the authentication message from a ling.
type ControlMessage struct {
	ServiceID string `json:"serviceId"`
	Token     string `json:"token"`
}

// ControlResponse is the server's response to authentication.
type ControlResponse struct {
	OK bool `json:"ok"`
}

// ServiceAuth holds the authentication token for a service.
type ServiceAuth struct {
	Token string
}

// TunnelServer manages QUIC connections and TCP listeners for a single bind port.
type TunnelServer struct {
	bindPort        int
	tlsConfig       *tls.Config
	mu              sync.RWMutex
	services        map[string]ServiceAuth
	tcpListeners    map[int]net.Listener
	tcpServiceIDs   map[int]string
	quicConns       map[string]*quic.Conn
	drainNotify     chan struct{}
	onLingConnected func()
}

// NewTunnelServer creates a new TunnelServer for the given bind port.
func NewTunnelServer(
	bindPort int, tlsConfig *tls.Config,
) *TunnelServer {
	return &TunnelServer{
		bindPort:        bindPort,
		tlsConfig:       tlsConfig,
		mu:              sync.RWMutex{},
		services:        make(map[string]ServiceAuth),
		tcpListeners:    make(map[int]net.Listener),
		tcpServiceIDs:   make(map[int]string),
		quicConns:       make(map[string]*quic.Conn),
		drainNotify:     make(chan struct{}, 1),
		onLingConnected: nil,
	}
}

func (tunnelSrv *TunnelServer) updateServices(
	services map[string]ServiceAuth,
) {
	tunnelSrv.mu.Lock()
	defer tunnelSrv.mu.Unlock()

	tunnelSrv.services = services
}

func (tunnelSrv *TunnelServer) run(
	ctx context.Context,
) error {
	listener, err := quic.ListenAddr(
		net.JoinHostPort(
			"0.0.0.0",
			strconv.Itoa(tunnelSrv.bindPort),
		),
		tunnelSrv.tlsConfig,
		&quic.Config{
			KeepAlivePeriod:    quicKeepAlivePeriod,
			MaxIdleTimeout:     quicMaxIdleTimeout,
			MaxIncomingStreams: quicMaxIncomingStreams,
		},
	)
	if err != nil {
		return fmt.Errorf(
			"listening on QUIC address: %w", err,
		)
	}

	slog.Info(
		"QUIC listener started",
		"bind_port", tunnelSrv.bindPort,
	)

	go func() {
		<-ctx.Done()

		_ = listener.Close()
	}()

	for ctx.Err() == nil {
		conn, acceptErr := listener.Accept(ctx)
		if acceptErr != nil {
			slog.Error(
				"QUIC accept failed", "error", acceptErr,
			)

			continue
		}

		go tunnelSrv.handleConnection(ctx, conn)
	}

	return nil
}

func (tunnelSrv *TunnelServer) handleConnection(
	ctx context.Context,
	conn *quic.Conn,
) {
	validServiceIDs, handleOK := tunnelSrv.negotiateConnection(ctx, conn)
	if !handleOK {
		return
	}

	tunnelSrv.registerConnections(validServiceIDs, conn)

	if tunnelSrv.onLingConnected != nil {
		tunnelSrv.onLingConnected()
	}

	<-conn.Context().Done()

	tunnelSrv.unregisterConnections(validServiceIDs, conn)

	slog.Info(
		"Ling disconnected",
		"service_ids", validServiceIDs,
	)
}

func (tunnelSrv *TunnelServer) negotiateConnection(
	ctx context.Context,
	conn *quic.Conn,
) ([]string, bool) {
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		slog.Error(
			"Failed to accept control stream",
			"error", err,
		)

		return nil, false
	}

	messages, decodeOK := decodeControlMessages(
		stream, conn,
	)
	if !decodeOK {
		return nil, false
	}

	validServiceIDs, authOK := tunnelSrv.authenticateMessages(
		messages, stream, conn,
	)
	if !authOK {
		return nil, false
	}

	response := ControlResponse{OK: true}

	err = json.NewEncoder(stream).Encode(response)
	if err != nil {
		slog.Error(
			"Failed to send control response",
			"error", err,
		)

		return nil, false
	}

	slog.Info(
		"Ling authenticated",
		"service_ids", validServiceIDs,
	)

	return validServiceIDs, true
}

func decodeControlMessages(
	stream *quic.Stream,
	conn *quic.Conn,
) ([]ControlMessage, bool) {
	decoder := json.NewDecoder(stream)

	var messages []ControlMessage

	err := decoder.Decode(&messages)
	if err != nil {
		slog.Error(
			"Failed to decode control messages",
			"error", err,
		)

		_ = stream.Close()
		_ = conn.CloseWithError(
			1, "invalid control message",
		)

		return nil, false
	}

	return messages, true
}

func (tunnelSrv *TunnelServer) authenticateMessages(
	messages []ControlMessage,
	stream *quic.Stream,
	conn *quic.Conn,
) ([]string, bool) {
	tunnelSrv.mu.RLock()

	validServiceIDs := make([]string, 0, len(messages))

	for _, msg := range messages {
		auth, exists := tunnelSrv.services[msg.ServiceID]
		if !exists || auth.Token != msg.Token {
			tunnelSrv.mu.RUnlock()
			slog.Error(
				"Invalid token for service",
				"service_id", msg.ServiceID,
			)

			_ = stream.Close()
			_ = conn.CloseWithError(
				quicAuthFailCode, "authentication failed",
			)

			return nil, false
		}

		validServiceIDs = append(
			validServiceIDs, msg.ServiceID,
		)
	}

	tunnelSrv.mu.RUnlock()

	return validServiceIDs, true
}

func (tunnelSrv *TunnelServer) registerConnections(
	serviceIDs []string,
	conn *quic.Conn,
) {
	tunnelSrv.mu.Lock()

	for _, serviceID := range serviceIDs {
		tunnelSrv.quicConns[serviceID] = conn
	}

	tunnelSrv.mu.Unlock()
}

func (tunnelSrv *TunnelServer) unregisterConnections(
	serviceIDs []string,
	conn *quic.Conn,
) {
	tunnelSrv.mu.Lock()

	for _, serviceID := range serviceIDs {
		if tunnelSrv.quicConns[serviceID] == conn {
			delete(tunnelSrv.quicConns, serviceID)
		}
	}

	empty := len(tunnelSrv.quicConns) == 0
	tunnelSrv.mu.Unlock()

	if empty {
		select {
		case tunnelSrv.drainNotify <- struct{}{}:
		default:
		}
	}
}

func (tunnelSrv *TunnelServer) waitForDrain(
	ctx context.Context,
) {
	for {
		tunnelSrv.mu.RLock()
		empty := len(tunnelSrv.quicConns) == 0
		tunnelSrv.mu.RUnlock()

		if empty {
			return
		}

		select {
		case <-tunnelSrv.drainNotify:
		case <-ctx.Done():
			return
		}
	}
}

func (tunnelSrv *TunnelServer) ensureTCPListener(
	ctx context.Context,
	remotePort int,
	serviceID string,
) {
	tunnelSrv.mu.Lock()

	tunnelSrv.tcpServiceIDs[remotePort] = serviceID

	if _, exists := tunnelSrv.tcpListeners[remotePort]; exists {
		tunnelSrv.mu.Unlock()

		return
	}

	listenConfig := net.ListenConfig{
		Control:         nil,
		KeepAlive:       0,
		KeepAliveConfig: net.KeepAliveConfig{},
	}

	listener, err := listenConfig.Listen(
		ctx, "tcp",
		net.JoinHostPort(
			"0.0.0.0", strconv.Itoa(remotePort),
		),
	)
	if err != nil {
		tunnelSrv.mu.Unlock()
		slog.Error(
			"Failed to listen on remote port",
			"remote_port", remotePort, "error", err,
		)

		return
	}

	tunnelSrv.tcpListeners[remotePort] = listener
	tunnelSrv.mu.Unlock()

	slog.Info(
		"TCP listener started",
		"remote_port", remotePort,
		"service_id", serviceID,
	)

	go tunnelSrv.acceptTCPConnections(
		ctx, listener, remotePort,
	)
}

func (tunnelSrv *TunnelServer) acceptTCPConnections(
	ctx context.Context,
	listener net.Listener,
	remotePort int,
) {
	for {
		tcpConn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}

		tunnelSrv.mu.RLock()
		serviceID := tunnelSrv.tcpServiceIDs[remotePort]
		tunnelSrv.mu.RUnlock()

		go tunnelSrv.handleTCPConnection(
			ctx, tcpConn, serviceID,
		)
	}
}

func (tunnelSrv *TunnelServer) removeTCPListener(
	remotePort int,
) {
	tunnelSrv.mu.Lock()
	defer tunnelSrv.mu.Unlock()

	listener, exists := tunnelSrv.tcpListeners[remotePort]
	if exists {
		_ = listener.Close()

		delete(tunnelSrv.tcpListeners, remotePort)
		delete(tunnelSrv.tcpServiceIDs, remotePort)
		slog.Info(
			"TCP listener removed",
			"remote_port", remotePort,
		)
	}
}

func (tunnelSrv *TunnelServer) handleTCPConnection(
	ctx context.Context,
	tcpConn net.Conn,
	serviceID string,
) {
	defer func() { _ = tcpConn.Close() }()

	stream, ok := tunnelSrv.openServiceStream(ctx, serviceID)
	if !ok {
		return
	}

	err := writeServiceIDHeader(stream, serviceID)
	if err != nil {
		_ = stream.Close()

		return
	}

	bridgeStreams(stream, tcpConn)
}

func (tunnelSrv *TunnelServer) openServiceStream(
	ctx context.Context,
	serviceID string,
) (*quic.Stream, bool) {
	for range quicStreamOpenRetries {
		tunnelSrv.mu.RLock()
		quicConn, exists := tunnelSrv.quicConns[serviceID]
		tunnelSrv.mu.RUnlock()

		if !exists {
			select {
			case <-ctx.Done():
				return nil, false
			case <-time.After(quicStreamRetryDelay):
				continue
			}
		}

		select {
		case <-quicConn.Context().Done():
			select {
			case <-ctx.Done():
				return nil, false
			case <-time.After(quicStreamRetryDelay):
				continue
			}
		default:
		}

		streamCtx, cancel := context.WithTimeout(
			ctx, quicStreamOpenTimeout,
		)

		stream, err := quicConn.OpenStreamSync(streamCtx)

		cancel()

		if err == nil {
			return stream, true
		}
	}

	return nil, false
}

func writeServiceIDHeader(
	stream *quic.Stream, serviceID string,
) error {
	serviceIDBytes := []byte(serviceID)

	serviceIDLen := len(serviceIDBytes)
	if serviceIDLen > math.MaxUint16 {
		return fmt.Errorf(
			"%w: %d bytes", errServiceIDTooLong,
			serviceIDLen,
		)
	}

	header := make([]byte, serviceIDHeaderBytes)
	binary.BigEndian.PutUint16(
		header, uint16(serviceIDLen),
	)

	_, err := stream.Write(header)
	if err != nil {
		return fmt.Errorf("writing header: %w", err)
	}

	_, err = stream.Write(serviceIDBytes)
	if err != nil {
		return fmt.Errorf(
			"writing service ID: %w", err,
		)
	}

	return nil
}

func bridgeStreams(
	stream *quic.Stream, tcpConn net.Conn,
) {
	done := make(chan struct{})

	go func() {
		_, _ = io.Copy(stream, tcpConn)
		_ = stream.Close()

		close(done)
	}()

	_, _ = io.Copy(tcpConn, stream)
	stream.CancelRead(0)
	<-done
}

func (tunnelSrv *TunnelServer) close() {
	tunnelSrv.mu.Lock()
	defer tunnelSrv.mu.Unlock()

	for port, listener := range tunnelSrv.tcpListeners {
		_ = listener.Close()

		delete(tunnelSrv.tcpListeners, port)
		delete(tunnelSrv.tcpServiceIDs, port)
	}

	for serviceID, conn := range tunnelSrv.quicConns {
		_ = conn.CloseWithError(
			0, "server shutting down",
		)

		delete(tunnelSrv.quicConns, serviceID)
	}
}
