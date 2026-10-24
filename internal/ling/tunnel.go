package ling

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/firecow/burrow/internal/bridge"
	"github.com/quic-go/quic-go"
)

const (
	quicKeepAlivePeriod   = 2 * time.Second
	quicMaxIdleTimeout    = 4 * time.Second
	quicMaxIncomingStream = 1024
	quicErrorRegFailed    = 1
	quicErrorRejected     = 2
	serviceIDHeaderBytes  = 2
)

var (
	errKingNoCertificate        = errors.New("king has no certificate")
	errKingRejectedRegistration = errors.New("king rejected registration")
)

type tunnelControlMessage struct {
	ServiceID string `json:"serviceId"`
	Token     string `json:"token"`
}

type tunnelControlResponse struct {
	OK bool `json:"ok"`
}

// TunnelService holds connection details for a single tunneled service.
type TunnelService struct {
	serviceID string
	token     string
	localAddr string
}

type kingConnection struct {
	conn    *quic.Conn
	streams *activity
}

// TunnelClient manages QUIC connections to king nodes.
type TunnelClient struct {
	mu          sync.Mutex
	connections map[string]kingConnection
	onConnected func()
}

// NewTunnelClient creates a new TunnelClient with an initialized connection map.
func NewTunnelClient() *TunnelClient {
	return &TunnelClient{
		mu:          sync.Mutex{},
		connections: make(map[string]kingConnection),
		onConnected: nil,
	}
}

func (tc *TunnelClient) ensureConnection(
	ctx context.Context,
	group *KingGroup,
	localAddrs map[string]string,
) {
	kingAddr := net.JoinHostPort(
		group.host, strconv.Itoa(group.bindPort),
	)

	if tc.hasActiveConnection(kingAddr) {
		return
	}

	conn, err := tc.dialKing(ctx, kingAddr, group.certPEM)
	if err != nil {
		slog.Error(
			"Failed to connect to king",
			"king_addr", kingAddr, "error", err,
		)

		return
	}

	err = tc.authenticateServices(ctx, conn, kingAddr, group.services)
	if err != nil {
		slog.Error(
			"Failed to authenticate with king",
			"king_addr", kingAddr, "error", err,
		)

		return
	}

	kingConn := kingConnection{conn: conn, streams: newActivity()}

	tc.mu.Lock()
	tc.connections[kingAddr] = kingConn
	tc.mu.Unlock()

	if tc.onConnected != nil {
		tc.onConnected()
	}

	go tc.acceptDataStreams(ctx, kingConn, kingAddr, localAddrs)
}

func (tc *TunnelClient) hasActiveConnection(kingAddr string) bool {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	kingConn, exists := tc.connections[kingAddr]
	if !exists {
		return false
	}

	select {
	case <-kingConn.conn.Context().Done():
		delete(tc.connections, kingAddr)

		return false
	default:
		return true
	}
}

func (tc *TunnelClient) dialKing(
	ctx context.Context,
	kingAddr string,
	certPEM string,
) (*quic.Conn, error) {
	if certPEM == "" {
		return nil, fmt.Errorf(
			"king at %s: %w", kingAddr, errKingNoCertificate,
		)
	}

	certPool := x509.NewCertPool()
	certPool.AppendCertsFromPEM([]byte(certPEM))

	tlsConfig := &tls.Config{
		RootCAs:    certPool,
		ServerName: "burrow",
		NextProtos: []string{"burrow"},
		MinVersion: tls.VersionTLS13,
	}

	conn, err := quic.DialAddr(
		ctx, kingAddr, tlsConfig,
		&quic.Config{
			KeepAlivePeriod:    quicKeepAlivePeriod,
			MaxIdleTimeout:     quicMaxIdleTimeout,
			MaxIncomingStreams: quicMaxIncomingStream,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("dialing king: %w", err)
	}

	return conn, nil
}

func (tc *TunnelClient) authenticateServices(
	ctx context.Context,
	conn *quic.Conn,
	kingAddr string,
	services []TunnelService,
) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(
			quicErrorRegFailed, "failed to open control stream",
		)

		return fmt.Errorf("opening control stream: %w", err)
	}

	messages := make([]tunnelControlMessage, 0, len(services))

	for _, svc := range services {
		messages = append(messages, tunnelControlMessage{
			ServiceID: svc.serviceID,
			Token:     svc.token,
		})
	}

	err = json.NewEncoder(stream).Encode(messages)
	if err != nil {
		_ = conn.CloseWithError(
			quicErrorRegFailed, "failed to send control messages",
		)

		return fmt.Errorf("sending control messages: %w", err)
	}

	var response tunnelControlResponse

	err = json.NewDecoder(stream).Decode(&response)
	if err != nil {
		_ = conn.CloseWithError(
			quicErrorRegFailed, "failed to read control response",
		)

		return fmt.Errorf("reading control response: %w", err)
	}

	if !response.OK {
		_ = conn.CloseWithError(
			quicErrorRejected, "registration rejected",
		)

		return fmt.Errorf(
			"king at %s: %w", kingAddr, errKingRejectedRegistration,
		)
	}

	slog.Info("Connected to king", "king_addr", kingAddr)

	return nil
}

func (tc *TunnelClient) acceptDataStreams(
	ctx context.Context,
	kingConn kingConnection,
	kingAddr string,
	localAddrs map[string]string,
) {
	serveCtx := context.WithoutCancel(ctx)

	for {
		dataStream, err := kingConn.conn.AcceptStream(serveCtx)
		if err != nil {
			slog.Info("King connection closed", "king_addr", kingAddr)

			tc.mu.Lock()
			if tc.connections[kingAddr].conn == kingConn.conn {
				delete(tc.connections, kingAddr)
			}
			tc.mu.Unlock()

			return
		}

		kingConn.streams.begin()

		go func() {
			defer kingConn.streams.end()

			handleDataStream(serveCtx, dataStream, localAddrs)
		}()
	}
}

func (tc *TunnelClient) retireUnneeded(ctx context.Context, groups map[string]*KingGroup) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	for addr, kingConn := range tc.connections {
		if _, needed := groups[addr]; needed {
			continue
		}

		delete(tc.connections, addr)

		go func() {
			drainCtx, drainCancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				streamDrainTimeout,
			)
			defer drainCancel()

			drainAndClose(drainCtx, kingConn, "no longer needed")
		}()
	}
}

func drainAndClose(ctx context.Context, kingConn kingConnection, reason string) {
	kingConn.streams.wait(ctx)

	_ = kingConn.conn.CloseWithError(quicErrorCodeCloseClean, reason)
}

func readServiceID(stream *quic.Stream) (string, error) {
	headerBuf := make([]byte, serviceIDHeaderBytes)

	_, err := io.ReadFull(stream, headerBuf)
	if err != nil {
		return "", fmt.Errorf("reading header: %w", err)
	}

	serviceIDLen := binary.BigEndian.Uint16(headerBuf)
	serviceIDBuf := make([]byte, serviceIDLen)

	_, err = io.ReadFull(stream, serviceIDBuf)
	if err != nil {
		return "", fmt.Errorf("reading service ID: %w", err)
	}

	return string(serviceIDBuf), nil
}

func handleDataStream(
	ctx context.Context,
	stream *quic.Stream,
	localAddrs map[string]string,
) {
	serviceID, err := readServiceID(stream)
	if err != nil {
		_ = stream.Close()

		return
	}

	localAddr, addrFound := localAddrs[serviceID]
	if !addrFound {
		_ = stream.Close()

		return
	}

	dialer := net.Dialer{
		Timeout: 0,
	}

	localConn, err := dialer.DialContext(ctx, "tcp", localAddr)
	if err != nil {
		slog.Error(
			"Failed to dial local service",
			"local_addr", localAddr, "error", err,
		)

		_ = stream.Close()

		return
	}

	bridge.Stream(stream, localConn)

	_ = localConn.Close()
}

func (tc *TunnelClient) closeAll(ctx context.Context) {
	tc.mu.Lock()
	kingConns := tc.connections
	tc.connections = make(map[string]kingConnection)
	tc.mu.Unlock()

	var drainWg sync.WaitGroup

	for _, kingConn := range kingConns {
		drainWg.Go(func() {
			drainAndClose(ctx, kingConn, "shutting down")
		})
	}

	drainWg.Wait()
}
