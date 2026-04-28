package cluster

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/paularlott/gossip"
	"github.com/paularlott/gossip/codec"
	"github.com/paularlott/logger"

	"praxis/internal/bot"
	"praxis/internal/sandbox"
)

// Config holds the parameters needed to start the watchdog cluster node.
type Config struct {
	// BindAddr is "host:port" for this gossip node (e.g. "0.0.0.0:7700").
	BindAddr string
	// AdvertiseAddr is the address announced to peers. Defaults to BindAddr.
	AdvertiseAddr string
	// Seeds are peer addresses to join on startup.
	Seeds []string
	// GlobalSecret is the fallback secret for bots that have no workspace secret.
	GlobalSecret string
	// ExtraMounts is the BOT_SHELL_MOUNTS value passed to the sandbox factory.
	ExtraMounts string
	// ShellAllowlist restricts which commands bots may execute via the proxy.
	// Empty means all commands are allowed (except blocked ones).
	ShellAllowlist []string
	// AuthDisabled explicitly disables gossip secret validation.
	// When false (default), requests are rejected if no secret is configured anywhere.
	AuthDisabled bool
}

// ConfigFromEnv builds a Config from well-known environment variables.
//
//	BOT_WATCHDOG_PORT  listen port (default 7700)
//	BOT_WATCHDOG_ADDR  advertise address (defaults to bind addr)
//	BOT_GLOBAL_SECRET  fallback gossip secret
//	BOT_SHELL_MOUNTS   extra sandbox mounts
//	BOT_SHELL_ALLOWLIST comma-separated command allowlist (empty = all allowed)
//	BOT_AUTH_DISABLED  set to "true" to disable secret validation
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

// Node is the watchdog gossip node. It owns the gossip.Cluster and registers
// all application message handlers.
type Node struct {
	cluster *gossip.Cluster
	manager *bot.Manager
	sandbox sandbox.Sandbox
	log     logger.Logger
	cfg     Config
}

// New creates a Node but does not start it. Call Start to join the cluster.
func New(cfg Config, mgr *bot.Manager, sb sandbox.Sandbox, log logger.Logger) (*Node, error) {
	gcfg := gossip.DefaultConfig()
	gcfg.BindAddr = cfg.BindAddr
	gcfg.AdvertiseAddr = cfg.AdvertiseAddr
	gcfg.MsgCodec = codec.NewVmihailencoMsgpackCodec()
	gcfg.Transport = gossip.NewSocketTransport(gcfg)
	gcfg.Logger = log

	gc, err := gossip.NewCluster(gcfg)
	if err != nil {
		return nil, fmt.Errorf("gossip cluster: %w", err)
	}

	n := &Node{
		cluster: gc,
		manager: mgr,
		sandbox: sb,
		log:     log,
		cfg:     cfg,
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

	// Join seed peers if provided.
	if len(n.cfg.Seeds) > 0 {
		if err := n.cluster.Join(n.cfg.Seeds); err != nil {
			n.log.Warn("could not join seed peers", "err", err)
		}
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
	default:
		n.log.Warn("bot_msg: unknown type", "type", hdr.Type)
		return &ShellReply{Error: "unknown message type: " + hdr.Type, ExitCode: 1}, nil
	}
}
