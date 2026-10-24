// Package bridge copies between connection pairs, half-closing on a clean end and aborting both directions on error.
package bridge

import (
	"io"
	"net"

	"github.com/quic-go/quic-go"
)

// Stream bridges a QUIC stream and a TCP connection until both directions have ended.
func Stream(stream *quic.Stream, conn net.Conn) {
	done := make(chan struct{})

	go func() {
		defer close(done)

		_, err := io.Copy(stream, conn)
		if err != nil {
			stream.CancelWrite(0)
			stream.CancelRead(0)

			return
		}

		_ = stream.Close()
	}()

	_, err := io.Copy(conn, stream)
	if err != nil {
		_ = conn.Close()
	} else {
		closeWrite(conn)
	}

	<-done
}

// Conns bridges two TCP connections until both directions have ended.
func Conns(client, upstream net.Conn) {
	done := make(chan struct{})

	go func() {
		defer close(done)

		copyHalf(upstream, client)
	}()

	copyHalf(client, upstream)

	<-done
}

func copyHalf(destination, source net.Conn) {
	_, err := io.Copy(destination, source)
	if err != nil {
		_ = destination.Close()
		_ = source.Close()

		return
	}

	closeWrite(destination)
}

func closeWrite(conn net.Conn) {
	tcpConn, isTCP := conn.(*net.TCPConn)
	if isTCP {
		_ = tcpConn.CloseWrite()
	}
}
