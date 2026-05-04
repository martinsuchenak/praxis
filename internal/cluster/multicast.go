package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/paularlott/logger"
)

const (
	defaultMCGroup = "239.255.13.37"
	defaultMCPort  = 19373
)

type multicastConfig struct {
	Group string
	Port  int
}

type discoverer struct {
	cfg     multicastConfig
	advAddr string
	log     logger.Logger

	mu     sync.Mutex
	joined bool
}

func defaultMulticastConfig() multicastConfig {
	return multicastConfig{
		Group: defaultMCGroup,
		Port:  defaultMCPort,
	}
}

func (d *discoverer) run(ctx context.Context, joinFunc func(addrs []string) error) {
	groupAddr := &net.UDPAddr{
		IP:   net.ParseIP(d.cfg.Group),
		Port: d.cfg.Port,
	}

	conn, err := net.ListenPacket("udp4", fmt.Sprintf("0.0.0.0:%d", d.cfg.Port))
	if err != nil {
		d.log.Warn("multicast: listen failed, auto-discovery disabled", "err", err)
		return
	}
	defer conn.Close()

	pconn := ipv4.NewPacketConn(conn)

	intf, err := multicastInterface()
	if err != nil {
		d.log.Warn("multicast: no suitable interface", "err", err)
		return
	}

	if err := pconn.JoinGroup(intf, groupAddr); err != nil {
		d.log.Warn("multicast: join group failed", "err", err)
		return
	}
	defer pconn.LeaveGroup(intf, groupAddr)

	if err := pconn.SetControlMessage(ipv4.FlagDst, true); err != nil {
		d.log.Debug("multicast: control message setup failed", "err", err)
	}

	buf := make([]byte, 1500)
	announceTicker := time.NewTicker(5 * time.Second)
	defer announceTicker.Stop()

	d.sendAnnounce(conn, groupAddr)

	for {
		select {
		case <-ctx.Done():
			return
		case <-announceTicker.C:
			d.mu.Lock()
			shouldAnnounce := !d.joined
			d.mu.Unlock()
			if shouldAnnounce {
				d.sendAnnounce(conn, groupAddr)
			}
		default:
		}

		if err := conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return
		}
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return
			}
			continue
		}

		var msg map[string]interface{}
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}

		peerAddr := handleDiscoverMsg(msg, d.advAddr)
		if peerAddr == "" {
			continue
		}

		d.log.Info("multicast: discovered peer", "addr", peerAddr)

		d.mu.Lock()
		if !d.joined {
			d.joined = true
			d.mu.Unlock()
			if err := joinFunc([]string{peerAddr}); err != nil {
				d.log.Warn("multicast: join failed", "addr", peerAddr, "err", err)
				d.mu.Lock()
				d.joined = false
				d.mu.Unlock()
			}
		} else {
			d.mu.Unlock()
		}
	}
}

func (d *discoverer) sendAnnounce(conn net.PacketConn, addr net.Addr) {
	msg, _ := json.Marshal(map[string]string{
		"type":        "discover",
		"gossip_addr": d.advAddr,
		"role":        "watchdog",
	})
	if _, err := conn.WriteTo(msg, addr); err != nil {
		d.log.Debug("multicast: announce failed", "err", err)
	}
}

// handleDiscoverMsg extracts the peer gossip address from a multicast message.
// Returns empty string if the message should be ignored (wrong type, missing
// address, or self-announcement).
func handleDiscoverMsg(msg map[string]interface{}, selfAddr string) string {
	if msg["type"] != "discover" {
		return ""
	}
	peerAddr, _ := msg["gossip_addr"].(string)
	if peerAddr == "" || peerAddr == selfAddr {
		return ""
	}
	return peerAddr
}

func multicastInterface() (*net.Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagMulticast == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil || len(addrs) == 0 {
			continue
		}
		return &i, nil
	}
	return nil, fmt.Errorf("no suitable multicast interface")
}

func startDiscovery(ctx context.Context, cfg multicastConfig, advAddr string, log logger.Logger, joinFunc func(addrs []string) error) {
	if cfg.Group == "" || cfg.Port == 0 {
		return
	}
	d := &discoverer{
		cfg:     cfg,
		advAddr: advAddr,
		log:     log,
	}
	go d.run(ctx, joinFunc)
}
