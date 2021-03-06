package grpcquic

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	qnet "grpc-quic/net"
	options "grpc-quic/opts"
	"grpc-quic/transports"
	quic "github.com/lucas-clemente/quic-go"
	ma "github.com/multiformats/go-multiaddr"
	"google.golang.org/grpc"
)

var quicConfig = &quic.Config{
	// MaxReceiveStreamFlowControlWindow:     3 * (1 << 20),   // 3 MB
	// MaxReceiveConnectionFlowControlWindow: 4.5 * (1 << 20), // 4.5 MB
	// Versions: []quic.VersionNumber{101},
	// AcceptCookie: func(clientAddr net.Addr, cookie *quic.Cookie) bool {
	// 	// TODO(#6): require source address validation when under load
	// 	return true
	// },
	KeepAlive: true,
}

func newPacketConn(addr string) (net.PacketConn, error) {
	// create a packet conn for outgoing connections
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}

	return net.ListenUDP("udp", udpAddr)
}

func newQuicDialer(tlsConf *tls.Config) func(string, time.Duration) (net.Conn, error) {
	return func(target string, timeout time.Duration) (net.Conn, error) {
		var err error

		m, err := ma.NewMultiaddr(target)
		if err != nil {
			return nil, err
		}

		laddr, protocol, err := qnet.ParseMultiaddr(m)
		if err != nil {
			return nil, err
		}

		if protocol == ma.P_UDP {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			sess, err := quic.DialAddrContext(ctx, laddr, tlsConf, quicConfig)
			if err != nil {
				return nil, err
			}

			return qnet.NewConn(sess)
		}

		if protocol == ma.P_TCP {
			return net.DialTimeout("tcp", laddr, timeout)
		}

		return nil, fmt.Errorf("Invalid protocol")
	}
}

func Dial(target string, opts ...options.DialOption) (*grpc.ClientConn, error) {
	cfg := options.NewClientConfig()
	if err := cfg.Apply(opts...); err != nil {
		return nil, err
	}

	creds := transports.NewCredentials(cfg.TLSConf)
	dialer := newQuicDialer(cfg.TLSConf)
	grpcOpts := []grpc.DialOption{
		grpc.WithDialer(dialer),
		grpc.WithTransportCredentials(creds),
	}

	grpcOpts = append(grpcOpts, cfg.GrpcDialOptions...)
	return grpc.Dial(target, grpcOpts...)
}

func newListener(laddr string, tlsConf *tls.Config) (net.Listener, error) {
	m, err := ma.NewMultiaddr(laddr)
	if err != nil {
		return nil, err
	}

	laddr, protocol, err := qnet.ParseMultiaddr(m)
	if err != nil {
		return nil, err
	}

	if protocol == ma.P_UDP {
		pconn, err := newPacketConn(laddr)
		if err != nil {
			return nil, err
		}

		ql, err := quic.Listen(pconn, tlsConf, quicConfig)
		if err != nil {
			return nil, err
		}

		return qnet.Listen(ql), nil
	}

	if protocol == ma.P_TCP {
		l, err := net.Listen("tcp", laddr)
		if err != nil {
			return nil, err
		}
		return l, nil
	}

	return nil, fmt.Errorf("Invalid protocol `%s`", m)
}

func NewServer(laddr string, opts ...options.ServerOption) (*grpc.Server, net.Listener, error) {
	cfg := options.NewServerConfig()
	if err := cfg.Apply(opts...); err != nil {
		return nil, nil, err
	}

	creds := transports.NewCredentials(cfg.TLSConf)
	l, err := newListener(laddr, cfg.TLSConf)
	if err != nil {
		return nil, nil, err
	}

	return grpc.NewServer(grpc.Creds(creds)), l, err
}
