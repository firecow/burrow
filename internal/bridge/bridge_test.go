package bridge_test

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/firecow/burrow/internal/bridge"
)

func connectedPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()

	listenConfig := net.ListenConfig{
		Control:         nil,
		KeepAlive:       0,
		KeepAliveConfig: net.KeepAliveConfig{},
	}

	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = listener.Close() }()

	dialer := net.Dialer{Timeout: time.Second}

	dialed, err := dialer.DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	accepted, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}

	dialedTCP, _ := dialed.(*net.TCPConn)
	acceptedTCP, _ := accepted.(*net.TCPConn)

	t.Cleanup(func() { _, _ = dialedTCP.Close(), acceptedTCP.Close() })

	return dialedTCP, acceptedTCP
}

func TestConns_AbortReachesOtherSide(t *testing.T) {
	t.Parallel()

	client, proxyClient := connectedPair(t)
	proxyUpstream, upstream := connectedPair(t)

	go bridge.Conns(proxyClient, proxyUpstream)

	_ = upstream.SetLinger(0)
	_ = upstream.Close()

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err := client.Read(make([]byte, 1))
	if errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal("expected the upstream abort to close the client connection")
	}
}

func TestConns_HalfCloseKeepsResponseFlowing(t *testing.T) {
	t.Parallel()

	client, proxyClient := connectedPair(t)
	proxyUpstream, upstream := connectedPair(t)

	go bridge.Conns(proxyClient, proxyUpstream)

	_, _ = client.Write([]byte("request"))
	_ = client.CloseWrite()

	request, _ := io.ReadAll(upstream)
	_, _ = upstream.Write([]byte("response to " + string(request)))
	_ = upstream.Close()

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	response, err := io.ReadAll(client)
	if err != nil || string(response) != "response to request" {
		t.Fatalf("expected full response after half-close, got %q (%v)", response, err)
	}
}
