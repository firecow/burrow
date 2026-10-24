package ling

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	proxyTargetWaitTimeout = 30 * time.Second
	dialMaxRetries         = 3
	dialRetryDelay         = 100 * time.Millisecond
)

// ProxyTarget holds the address of a single upstream target.
type ProxyTarget struct {
	host       string
	remotePort int
}

func proxyTargetAddr(target ProxyTarget) string {
	return net.JoinHostPort(target.host, strconv.Itoa(target.remotePort))
}

// TCPProxy is a round-robin TCP proxy that forwards connections to upstream targets.
type TCPProxy struct {
	name       string
	bindPort   int
	mu         sync.RWMutex
	targets    []ProxyTarget
	counter    atomic.Uint64
	listener   net.Listener
	acceptDone chan struct{}
	clients    *activity
	closeOnce  sync.Once
	hasTargets chan struct{}

	upstreamMu sync.Mutex
	upstreams  map[string][]net.Conn
}

// NewTCPProxy creates a new TCPProxy with the given name and bind port.
func NewTCPProxy(name string, bindPort int) *TCPProxy {
	return &TCPProxy{
		name:       name,
		bindPort:   bindPort,
		mu:         sync.RWMutex{},
		targets:    nil,
		counter:    atomic.Uint64{},
		listener:   nil,
		acceptDone: make(chan struct{}),
		clients:    newActivity(),
		closeOnce:  sync.Once{},
		hasTargets: make(chan struct{}),
		upstreamMu: sync.Mutex{},
		upstreams:  make(map[string][]net.Conn),
	}
}

// ReadTargets returns a snapshot of the current proxy targets (thread-safe).
func (proxy *TCPProxy) ReadTargets() []ProxyTarget {
	proxy.mu.RLock()
	defer proxy.mu.RUnlock()

	return proxy.targets
}

func (proxy *TCPProxy) updateTargets(targets []ProxyTarget) {
	proxy.mu.Lock()

	proxy.targets = targets

	if len(targets) > 0 {
		select {
		case <-proxy.hasTargets:
		default:
			close(proxy.hasTargets)
		}
	} else {
		select {
		case <-proxy.hasTargets:
			proxy.hasTargets = make(chan struct{})
		default:
		}
	}

	proxy.mu.Unlock()
}

func (proxy *TCPProxy) trackUpstream(addr string, conn net.Conn) {
	proxy.upstreamMu.Lock()
	proxy.upstreams[addr] = append(proxy.upstreams[addr], conn)
	proxy.upstreamMu.Unlock()
}

func (proxy *TCPProxy) untrackUpstream(addr string, conn net.Conn) {
	proxy.upstreamMu.Lock()

	conns := proxy.upstreams[addr]

	for index, candidate := range conns {
		if candidate == conn {
			proxy.upstreams[addr] = append(
				conns[:index], conns[index+1:]...,
			)

			break
		}
	}

	proxy.upstreamMu.Unlock()
}

func (proxy *TCPProxy) start(ctx context.Context) error {
	listenConfig := net.ListenConfig{
		KeepAlive: 0,
	}

	listener, err := listenConfig.Listen(
		ctx, "tcp",
		net.JoinHostPort("0.0.0.0", strconv.Itoa(proxy.bindPort)),
	)
	if err != nil {
		return fmt.Errorf(
			"listening on port %d: %w", proxy.bindPort, err,
		)
	}

	proxy.listener = listener

	slog.Info(
		"TCP proxy started",
		"name", proxy.name, "bind_port", proxy.bindPort,
	)

	serveCtx := context.WithoutCancel(ctx)

	go func() {
		defer close(proxy.acceptDone)

		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			proxy.clients.begin()

			go func() {
				defer proxy.clients.end()

				proxy.handleConn(serveCtx, conn)
			}()
		}
	}()

	return nil
}

func (proxy *TCPProxy) handleConn(
	ctx context.Context,
	clientConn net.Conn,
) {
	defer func() { _ = clientConn.Close() }()

	upstream, addr, ok := proxy.dialUpstream(ctx)
	if !ok {
		return
	}

	defer func() { _ = upstream.Close() }()

	proxy.trackUpstream(addr, upstream)

	defer proxy.untrackUpstream(addr, upstream)

	done := make(chan struct{})

	go func() {
		_, _ = io.Copy(upstream, clientConn)
		closeWrite(upstream)

		close(done)
	}()

	_, _ = io.Copy(clientConn, upstream)
	closeWrite(clientConn)

	<-done
}

func closeWrite(conn net.Conn) {
	tcpConn, isTCP := conn.(*net.TCPConn)
	if isTCP {
		_ = tcpConn.CloseWrite()
	}
}

func (proxy *TCPProxy) dialUpstream(
	ctx context.Context,
) (net.Conn, string, bool) {
	dialer := net.Dialer{
		Timeout: 0,
	}

	for attempt := range dialMaxRetries {
		target, found := proxy.waitForTarget()
		if !found {
			return nil, "", false
		}

		addr := proxyTargetAddr(target)

		upstream, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			return upstream, addr, true
		}

		slog.Error(
			"Failed to dial upstream",
			"name", proxy.name, "addr", addr,
			"error", err, "attempt", attempt+1,
		)

		select {
		case <-ctx.Done():
			return nil, "", false
		case <-time.After(dialRetryDelay):
		}
	}

	return nil, "", false
}

func (proxy *TCPProxy) waitForTarget() (ProxyTarget, bool) {
	deadline := time.After(proxyTargetWaitTimeout)

	for {
		proxy.mu.RLock()
		targets := proxy.targets
		hasTargets := proxy.hasTargets
		proxy.mu.RUnlock()

		if len(targets) > 0 {
			idx := proxy.counter.Add(1) - 1

			return targets[idx%uint64(len(targets))], true
		}

		select {
		case <-hasTargets:
			continue
		case <-deadline:
			return ProxyTarget{host: "", remotePort: 0}, false
		}
	}
}

func (proxy *TCPProxy) close(ctx context.Context) {
	proxy.closeOnce.Do(func() {
		if proxy.listener != nil {
			_ = proxy.listener.Close()

			<-proxy.acceptDone
		}

		proxy.clients.wait(ctx)

		proxy.mu.Lock()

		select {
		case <-proxy.hasTargets:
		default:
			close(proxy.hasTargets)
		}

		proxy.mu.Unlock()

		proxy.upstreamMu.Lock()

		for addr, conns := range proxy.upstreams {
			for _, conn := range conns {
				_ = conn.Close()
			}

			delete(proxy.upstreams, addr)
		}

		proxy.upstreamMu.Unlock()
	})
}
