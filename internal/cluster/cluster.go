package cluster

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/paularlott/gossip"
	"github.com/paularlott/gossip/codec"
	"github.com/paularlott/logger"
	"tailscale.com/tsnet"

	"praxis/internal/bot"
	"praxis/internal/sandbox"
)

// Config holds the parameters needed to start the watchdog cluster node.
type Config struct {
	BindAddr        string
	AdvertiseAddr   string
	Seeds           []string
	GlobalSecret    string
	ExtraMounts     string
	ShellAllowlist  []string
	AuthDisabled    bool
	NodeName        string
	MulticastAddr   string
	MulticastPort   int
	TsnetHostname   string
	TsnetDir        string
	TsnetAuthKey    string
	TsnetControlURL string
}

// ConfigFromEnv builds a Config from well-known environment variables.
//
//	BOT_WATCHDOG_PORT     listen port (default 7700)
//	BOT_WATCHDOG_ADDR     advertise address (defaults to bind addr)
//	BOT_GLOBAL_SECRET     fallback gossip secret
//	BOT_SHELL_MOUNTS      extra sandbox mounts
//	BOT_SHELL_ALLOWLIST   comma-separated command allowlist (empty = all allowed)
//	BOT_AUTH_DISABLED     set to "true" to disable secret validation
//	BOT_NODE_NAME         human-readable node name (default: advertise address)
//	BOT_MULTICAST_ADDR    multicast group for auto-discovery (default: 239.255.13.37)
//	BOT_MULTICAST_PORT    multicast port for auto-discovery (default: 19373)
func ConfigFromEnv() Config {
	port := os.Getenv("BOT_WATCHDOG_PORT")
	if port == "" {
		port = "7700"
	}
	bindAddr := "0.0.0.0:" + port

	advertise := os.Getenv("BOT_WATCHDOG_ADDR")
	if advertise == "" {
		advertise = bindAddr
	}

	return Config{
		BindAddr:       bindAddr,
		AdvertiseAddr:  advertise,
		GlobalSecret:   os.Getenv("BOT_GLOBAL_SECRET"),
		ExtraMounts:    os.Getenv("BOT_SHELL_MOUNTS"),
		ShellAllowlist: parseAllowlist(os.Getenv("BOT_SHELL_ALLOWLIST")),
		AuthDisabled:   os.Getenv("BOT_AUTH_DISABLED") == "true",
		NodeName:       os.Getenv("BOT_NODE_NAME"),
		MulticastAddr:  os.Getenv("BOT_MULTICAST_ADDR"),
		MulticastPort:  envInt("BOT_MULTICAST_PORT", defaultMCPort),
	}
}

func parseAllowlist(env string) []string {
	if env == "" {
		return nil
	}
	var list []string
	for _, s := range strings.Split(env, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			list = append(list, s)
		}
	}
	return list
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n := def
	fmt.Sscanf(v, "%d", &n)
	return n
}

// Node is the watchdog gossip node. It owns the gossip.Cluster and registers
// all application message handlers.
type Node struct {
	cluster  *gossip.Cluster
	manager  *bot.Manager
	sandbox  sandbox.Sandbox
	log      logger.Logger
	cfg      Config
	tsnetSrv *tsnet.Server
}

func New(cfg Config, mgr *bot.Manager, sb sandbox.Sandbox, log logger.Logger) (*Node, error) {
	gcfg := gossip.DefaultConfig()
	gcfg.BindAddr = cfg.BindAddr
	gcfg.AdvertiseAddr = cfg.AdvertiseAddr
	gcfg.MsgCodec = codec.NewVmihailencoMsgpackCodec()
	gcfg.Transport = gossip.NewSocketTransport(gcfg)
	gcfg.Logger = log

	var tsSrv *tsnet.Server
	if cfg.TsnetHostname != "" {
		var err error
		tsSrv, err = setupTsnetTransport(gcfg, tsnetConfig{
			Hostname:   cfg.TsnetHostname,
			Dir:        cfg.TsnetDir,
			AuthKey:    cfg.TsnetAuthKey,
			ControlURL: cfg.TsnetControlURL,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("tsnet: %w", err)
		}
	}

	gc, err := gossip.NewCluster(gcfg)
	if err != nil {
		if tsSrv != nil {
			_ = tsSrv.Close()
		}
		return nil, fmt.Errorf("gossip cluster: %w", err)
	}

	n := &Node{
		cluster:  gc,
		manager:  mgr,
		sandbox:  sb,
		log:      log,
		cfg:      cfg,
		tsnetSrv: tsSrv,
	}

	n.registerHandlers()

	return n, nil
}

// Start starts the gossip node and joins any seed peers.
func (n *Node) Start(ctx context.Context) error {
	n.cluster.Start()

	// Advertise the watchdog role so bots can find this node via _find_watchdog().
	n.cluster.LocalMetadata().SetString("role", "watchdog")
	// Advertise an id so bots display a readable sender name in their inbox.
	n.cluster.LocalMetadata().SetString("id", "operator")
	// Advertise node name so peers can target this node for remote spawn.
	nodeName := n.cfg.NodeName
	if nodeName == "" {
		nodeName = n.cfg.AdvertiseAddr
	}
	n.cluster.LocalMetadata().SetString("node_name", nodeName)

	// Join seed peers if provided.
	if len(n.cfg.Seeds) > 0 {
		if err := n.cluster.Join(n.cfg.Seeds); err != nil {
			n.log.Warn("could not join seed peers", "err", err)
		}
	} else {
		mcCfg := multicastConfig{
			Group: n.cfg.MulticastAddr,
			Port:  n.cfg.MulticastPort,
		}
		if mcCfg.Group == "" {
			mcCfg = defaultMulticastConfig()
		}
		startDiscovery(ctx, mcCfg, n.cfg.AdvertiseAddr, n.log, func(addrs []string) error {
			return n.cluster.Join(addrs)
		})
	}

	// Propagate context cancellation to cluster shutdown.
	go func() {
		<-ctx.Done()
		n.cluster.Stop()
	}()

	return nil
}

// Stop gracefully shuts down the gossip node.
func (n *Node) Stop() {
	n.cluster.Stop()
	if n.tsnetSrv != nil {
		_ = n.tsnetSrv.Close()
	}
}

// Cluster returns the underlying gossip.Cluster for event handler registration
// (e.g. TUI state/metadata change callbacks).
func (n *Node) Cluster() *gossip.Cluster {
	return n.cluster
}

// SendMessage sends a direct "message" payload to a named bot via gossip.
// Returns an error if the bot is not currently alive in the cluster.
func (n *Node) SendMessage(botName, content string) error {
	return n.sendToBot(botName, "message", map[string]interface{}{
		"content": content,
	})
}

// sendToBot looks up the named bot in the live cluster, attaches the
// appropriate gossip secret, and sends a user-message packet.
func (n *Node) sendToBot(botName, msgType string, extra map[string]interface{}) error {
	b, err := n.manager.Get(botName)
	if err != nil {
		return fmt.Errorf("bot %q: %w", botName, err)
	}

	secret := b.Config.GossipSecret
	if secret == "" {
		secret = n.cfg.GlobalSecret
	}

	payload := map[string]interface{}{"type": msgType}
	for k, v := range extra {
		payload[k] = v
	}
	if secret != "" {
		payload["_secret"] = secret
	}

	if n.cluster == nil {
		return fmt.Errorf("cluster not started")
	}
	for _, gn := range n.cluster.AliveNodes() {
		if gn.Metadata.GetString("id") == botName {
			return n.cluster.SendToReliable(gn, MsgBotToWatchdog, payload)
		}
	}
	return fmt.Errorf("bot %q not found in cluster (not alive)", botName)
}

// registerHandlers wires up the single bot→watchdog message dispatcher.
// All bot messages arrive at MsgBotToWatchdog (128) and are routed internally
// by the "type" field, matching botcore.py's GOSSIP_MSG = gossip.MSG_USER.
func (n *Node) registerHandlers() {
	_ = n.cluster.HandleFuncWithReply(MsgBotToWatchdog, n.handleBotMsg)
}

// handleBotMsg is the single entry point for all bot→watchdog messages.
// It peeks the "type" discriminator and dispatches to the appropriate handler.
func (n *Node) handleBotMsg(gn *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var hdr botRequest
	if err := pkt.Unmarshal(&hdr); err != nil {
		n.log.Warn("bot_msg: bad header", "err", err)
		return &ShellReply{Error: "bad request", ExitCode: 1}, nil
	}
	switch hdr.Type {
	case TypeShellReq:
		return n.handleShellReq(gn, pkt)
	case TypeSpawnReq:
		return n.handleSpawnReq(gn, pkt)
	case TypeRelayReq:
		return n.handleRelayReq(gn, pkt)
	case TypeRemoteSpawnReq:
		return n.handleRemoteSpawnReq(gn, pkt)
	case TypeTerminateReq:
		return n.handleTerminateReq(gn, pkt)
	case TypeHardwareReq:
		return n.handleHardwareReq(gn, pkt)
	default:
		n.log.Warn("bot_msg: unknown type", "type", hdr.Type)
		return &ShellReply{Error: "unknown message type: " + hdr.Type, ExitCode: 1}, nil
	}
}
