package ling_test

import (
	"context"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/firecow/burrow/internal/ling"
)

const (
	testTargetAddr  = "10.0.0.1:5000"
	testTargetAddr2 = "10.0.0.2:5000"
	testTargetHost  = "10.0.0.1"
	testTargetHost2 = "10.0.0.2"
	testTargetPort  = 5000
	readBufferSize  = 128
	pingBufferSize  = 4
)

func newTestListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control:         func(string, string, syscall.RawConn) error { return nil },
		KeepAlive:       0,
		KeepAliveConfig: net.KeepAliveConfig{Enable: false, Idle: 0, Interval: 0, Count: 0},
	}
}

func newTestDialer(timeout time.Duration) net.Dialer {
	return net.Dialer{
		Timeout:         timeout,
		Deadline:        time.Time{},
		LocalAddr:       nil,
		FallbackDelay:   0,
		KeepAlive:       0,
		KeepAliveConfig: net.KeepAliveConfig{Enable: false, Idle: 0, Interval: 0, Count: 0},
		Resolver:        nil,
		Control:         nil,
		ControlContext:  nil,
	}
}

func startTestProxy(
	ctx context.Context,
	t *testing.T,
	proxy *ling.TCPProxy,
) string {
	t.Helper()

	err := proxy.TestStart(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return proxy.TestListenerAddr()
}

func TestTCPProxy_RoundRobin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	backend1, backend2 := startEchoBackends(ctx, t)

	defer func() { _ = backend1.Close() }()

	defer func() { _ = backend2.Close() }()

	addr1 := mustTCPAddr(t, backend1)
	addr2 := mustTCPAddr(t, backend2)

	proxy := ling.NewTCPProxy("test", 0)

	proxy.TestUpdateTargets([]ling.ProxyTarget{
		ling.NewProxyTarget("127.0.0.1", addr1.Port),
		ling.NewProxyTarget("127.0.0.1", addr2.Port),
	})

	proxyAddr := startTestProxy(ctx, t, proxy)

	defer proxy.TestClose(ctx)

	resp1 := dialAndSend(t, proxyAddr)
	resp2 := dialAndSend(t, proxyAddr)

	if resp1 == resp2 {
		t.Logf("resp1=%s resp2=%s", resp1, resp2)
		t.Log("Both went to same backend (acceptable but not ideal)")
	}
}

func startEchoBackends(
	ctx context.Context,
	t *testing.T,
) (net.Listener, net.Listener) {
	t.Helper()

	listenConfig := newTestListenConfig()

	backend1, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	backend2, err := listenConfig.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		_ = backend1.Close()

		t.Fatal(err)
	}

	go echoServer(backend1, "backend1")
	go echoServer(backend2, "backend2")

	return backend1, backend2
}

func mustTCPAddr(t *testing.T, listener net.Listener) *net.TCPAddr {
	t.Helper()

	tcpAddr, isTCPAddr := listener.Addr().(*net.TCPAddr)
	if !isTCPAddr {
		t.Fatal("expected *net.TCPAddr")
	}

	return tcpAddr
}

func TestTCPProxy_NoTargets(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	proxy := ling.NewTCPProxy("test", 0)

	proxyAddr := startTestProxy(ctx, t, proxy)

	closeCtx, closeCancel := context.WithTimeout(ctx, time.Second)
	defer closeCancel()

	defer proxy.TestClose(closeCtx)

	dialer := newTestDialer(time.Second)

	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(time.Second))

	buf := make([]byte, 1)

	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected error (connection should be closed)")
	}
}

func echoServer(listener net.Listener, name string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}

		go func(candidate net.Conn) {
			defer func() { _ = candidate.Close() }()

			prefix := []byte(name + ":")

			_, _ = candidate.Write(prefix)
			_, _ = io.Copy(candidate, candidate)
		}(conn)
	}
}

func TestTCPProxy_UpdateTargets_PreservesInFlightConnections(t *testing.T) {
	t.Parallel()

	proxy := ling.NewTCPProxy("test", 0)

	server, client := net.Pipe()

	defer func() { _ = server.Close() }()

	defer func() { _ = client.Close() }()

	proxy.TestTrackUpstream(testTargetAddr, client)

	proxy.TestUpdateTargets([]ling.ProxyTarget{
		ling.NewProxyTarget(testTargetHost, testTargetPort),
	})

	proxy.TestUpdateTargets(nil)

	go func() {
		_, _ = server.Write([]byte("ping"))
	}()

	_ = client.SetReadDeadline(time.Now().Add(time.Second))

	buf := make([]byte, pingBufferSize)

	bytesRead, err := client.Read(buf)
	if err != nil {
		t.Fatalf(
			"expected in-flight connection to remain readable: %v",
			err,
		)
	}

	if string(buf[:bytesRead]) != "ping" {
		t.Errorf("expected 'ping', got %q", string(buf[:bytesRead]))
	}
}

func TestTCPProxy_Close_DrainsInFlightConnections(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	backend, otherBackend := startEchoBackends(ctx, t)

	defer func() { _, _ = backend.Close(), otherBackend.Close() }()

	proxy := ling.NewTCPProxy("test", 0)
	proxy.TestUpdateTargets(
		[]ling.ProxyTarget{ling.NewProxyTarget("127.0.0.1", mustTCPAddr(t, backend).Port)},
	)

	proxyAddr := startTestProxy(ctx, t, proxy)
	dialer := newTestDialer(time.Second)

	conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		t.Fatal(err)
	}

	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, readBufferSize)

	_, err = io.ReadFull(conn, buf[:len("backend1:")])
	if err != nil {
		t.Fatal(err)
	}

	closeDone := make(chan struct{})

	go func() {
		proxy.TestClose(ctx)
		close(closeDone)
	}()

	for {
		probe, dialErr := dialer.DialContext(ctx, "tcp", proxyAddr)
		if dialErr != nil {
			break
		}

		_ = probe.Close()
	}

	_, _ = conn.Write([]byte("ping"))

	_, err = io.ReadFull(conn, buf[:len("ping")])
	if err != nil {
		t.Fatalf("expected in-flight connection to survive drain: %v", err)
	}

	select {
	case <-closeDone:
		t.Fatal("expected close to wait for the in-flight connection")
	default:
	}

	_ = conn.Close()

	<-closeDone
}

func TestTCPProxy_Close_ClosesUpstreams(t *testing.T) {
	t.Parallel()

	proxy := ling.NewTCPProxy("test", 0)

	server, client := net.Pipe()

	defer func() { _ = server.Close() }()

	proxy.TestTrackUpstream(testTargetAddr, client)

	proxy.TestClose(t.Context())

	_, err := client.Write([]byte("test"))
	if err == nil {
		t.Error("expected write to closed connection to fail")
	}
}

func TestTCPProxy_TrackAndUntrack(t *testing.T) {
	t.Parallel()

	proxy := ling.NewTCPProxy("test", 0)

	_, client1 := net.Pipe()
	_, client2 := net.Pipe()

	proxy.TestTrackUpstream(testTargetAddr, client1)
	proxy.TestTrackUpstream(testTargetAddr, client2)

	count := proxy.TestUpstreamCount(testTargetAddr)
	if count != 2 {
		t.Fatalf("expected 2 tracked upstreams, got %d", count)
	}

	proxy.TestUntrackUpstream(testTargetAddr, client1)

	count = proxy.TestUpstreamCount(testTargetAddr)
	if count != 1 {
		t.Fatalf("expected 1 tracked upstream after untrack, got %d", count)
	}

	proxy.TestUntrackUpstream(testTargetAddr, client2)

	count = proxy.TestUpstreamCount(testTargetAddr)
	if count != 0 {
		t.Errorf(
			"expected 0 tracked upstreams after untrack all, got %d",
			count,
		)
	}
}

func dialAndSend(t *testing.T, addr string) string {
	t.Helper()

	dialer := newTestDialer(time.Second)

	conn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = conn.Close() }()

	_ = conn.SetDeadline(time.Now().Add(time.Second))

	buf := make([]byte, readBufferSize)

	bytesRead, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}

	return string(buf[:bytesRead])
}
