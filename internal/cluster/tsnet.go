package cluster

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/paularlott/gossip"
	"tailscale.com/tsnet"
)

type dualListener struct {
	local    net.Listener
	tsnet    net.Listener
	acceptCh chan acceptResult
	closed   chan struct{}
	once     sync.Once
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func newDualListener(local, ts net.Listener) *dualListener {
	dl := &dualListener{
		local:    local,
		tsnet:    ts,
		acceptCh: make(chan acceptResult),
		closed:   make(chan struct{}),
	}
	go dl.acceptLoop(dl.local)
	go dl.acceptLoop(dl.tsnet)
	return dl
}

func (d *dualListener) acceptLoop(l net.Listener) {
	for {
		conn, err := l.Accept()
		select {
		case <-d.closed:
			if conn != nil {
				_ = conn.Close()
			}
			return
		case d.acceptCh <- acceptResult{conn: conn, err: err}:
			if err != nil {
				return
			}
		}
	}
}

func (d *dualListener) Accept() (net.Conn, error) {
	r, ok := <-d.acceptCh
	if !ok {
		return nil, fmt.Errorf("listener closed")
	}
	return r.conn, r.err
}

func (d *dualListener) Close() error {
	d.once.Do(func() {
		close(d.closed)
		_ = d.local.Close()
		_ = d.tsnet.Close()
	})
	return nil
}

func (d *dualListener) Addr() net.Addr {
	return d.local.Addr()
}

type tsnetDialer struct {
	server              *tsnet.Server
	fallbackDialTimeout time.Duration
}

func (t *tsnetDialer) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip != nil && isCGNAT(ip) {
		return t.server.Dial(ctx, network, addr)
	}
	return net.DialTimeout(network, addr, t.fallbackDialTimeout)
}

func isCGNAT(ip net.IP) bool {
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return cgnat.Contains(ip)
}

type tsnetConfig struct {
	Hostname   string
	Dir        string
	AuthKey    string
	ControlURL string
}

func setupTsnetTransport(gcfg *gossip.Config, tsCfg tsnetConfig, log interface {
	Info(string, ...interface{})
}) (*tsnet.Server, error) {
	srv := &tsnet.Server{
		Hostname:   tsCfg.Hostname,
		Dir:        tsCfg.Dir,
		AuthKey:    tsCfg.AuthKey,
		ControlURL: tsCfg.ControlURL,
		Ephemeral:  tsCfg.AuthKey != "",
	}
	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("tsnet start: %w", err)
	}

	status, err := srv.Up(context.Background())
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("tsnet up: %w", err)
	}

	tsListener, err := srv.Listen("tcp", gcfg.BindAddr)
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("tsnet listen: %w", err)
	}

	localListener, err := net.Listen("tcp", gcfg.BindAddr)
	if err != nil {
		_ = tsListener.Close()
		_ = srv.Close()
		return nil, fmt.Errorf("local listen: %w", err)
	}

	dl := newDualListener(localListener, tsListener)

	dialer := &tsnetDialer{
		server:              srv,
		fallbackDialTimeout: gcfg.TCPDialTimeout,
	}

	gcfg.ListenFunc = func(network, addr string) (net.Listener, error) {
		return dl, nil
	}
	gcfg.DialFunc = dialer.dial

	var tailscaleAddrs []string
	for _, ip := range status.TailscaleIPs {
		tailscaleAddrs = append(tailscaleAddrs, ip.String())
	}

	log.Info("tsnet enabled",
		"hostname", tsCfg.Hostname,
		"tailscale_ips", fmt.Sprintf("%v", tailscaleAddrs),
	)

	return srv, nil
}
