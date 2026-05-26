package cmd

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/paularlott/cli"
	"github.com/paularlott/gossip"
	"github.com/paularlott/gossip/codec"

	"praxis/internal/cluster"
	"praxis/internal/config"
)

func defaultModel() string {
	cfg := config.Get()
	if cfg == nil {
		return os.Getenv("BOT_MODEL")
	}
	return cfg.Bot.Model
}

func defaultGlobalSecret() string {
	cfg := config.Get()
	if cfg == nil {
		return os.Getenv("BOT_GLOBAL_SECRET")
	}
	return cfg.Watchdog.Secret
}

func parseCSVFlag(val string) []string {
	if val == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(val, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func resolveWorkspace(name string) (path, gossipSecret, defaultScope string) {
	cfg := config.Get()
	if cfg != nil {
		p, s, sc, ok := cfg.ResolveWorkspace(name)
		if ok {
			return p, s, sc
		}
	}
	return "", "", ""
}

func remoteFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "node",
			Usage:   "Remote watchdog node name for cluster operations",
			EnvVars: []string{"BOT_NODE"},
		},
		&cli.StringFlag{
			Name:    "seeds",
			Usage:   "Comma-separated gossip seed addresses for remote operations",
			EnvVars: []string{"BOT_SEED_ADDRS"},
		},
	}
}

func resolveSeeds(cmd *cli.Command) []string {
	if s := cmd.GetString("seeds"); s != "" {
		return parseCSVFlag(s)
	}
	cfg := config.Get()
	if cfg != nil && len(cfg.Watchdog.Seeds) > 0 {
		return cfg.Watchdog.Seeds
	}
	return nil
}

func remoteControlBot(ctx context.Context, cmd *cli.Command, botID, action string) error {
	nodeName := cmd.GetString("node")
	if nodeName == "" {
		return fmt.Errorf("--node is required for remote operations")
	}
	seeds := resolveSeeds(cmd)
	if len(seeds) == 0 {
		return fmt.Errorf("--seeds is required for remote operations (or set BOT_SEED_ADDRS)")
	}
	secret := defaultGlobalSecret()

	gc, err := joinCluster(ctx, seeds)
	if err != nil {
		return err
	}
	defer gc.Stop()

	target, err := findWatchdogPeer(ctx, gc, nodeName)
	if err != nil {
		return err
	}

	req := map[string]interface{}{
		"type":    cluster.TypeBotControlReq,
		"bot_id":  botID,
		"action":  action,
		"_secret": secret,
	}

	var reply cluster.BotControlReply
	if err := gc.SendToWithResponse(target, gossip.UserMsg, req, &reply); err != nil {
		return fmt.Errorf("control bot: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("control bot: %s", reply.Error)
	}
	return nil
}

func remoteControlAll(ctx context.Context, cmd *cli.Command, action string) (int, error) {
	nodeName := cmd.GetString("node")
	if nodeName == "" {
		return 0, fmt.Errorf("--node is required for remote operations")
	}
	seeds := resolveSeeds(cmd)
	if len(seeds) == 0 {
		return 0, fmt.Errorf("--seeds is required for remote operations (or set BOT_SEED_ADDRS)")
	}
	secret := defaultGlobalSecret()

	gc, err := joinCluster(ctx, seeds)
	if err != nil {
		return 0, err
	}
	defer gc.Stop()

	target, err := findWatchdogPeer(ctx, gc, nodeName)
	if err != nil {
		return 0, err
	}

	req := map[string]interface{}{
		"type":    cluster.TypeBotControlReq,
		"bot_id":  "",
		"action":  action,
		"_secret": secret,
	}
	var reply cluster.BotControlReply
	if err := gc.SendToWithResponse(target, gossip.UserMsg, req, &reply); err != nil {
		return 0, fmt.Errorf("bulk control: %w", err)
	}
	if reply.Error != "" {
		return 0, fmt.Errorf("bulk control: %s", reply.Error)
	}
	return reply.Count, nil
}

func remoteListBots(ctx context.Context, cmd *cli.Command) error {
	nodeName := cmd.GetString("node")
	if nodeName == "" {
		return fmt.Errorf("--node is required for remote operations")
	}
	seeds := resolveSeeds(cmd)
	if len(seeds) == 0 {
		return fmt.Errorf("--seeds is required for remote operations (or set BOT_SEED_ADDRS)")
	}
	secret := defaultGlobalSecret()

	gc, err := joinCluster(ctx, seeds)
	if err != nil {
		return err
	}
	defer gc.Stop()

	target, err := findWatchdogPeer(ctx, gc, nodeName)
	if err != nil {
		return err
	}

	req := map[string]interface{}{
		"type":    cluster.TypeListBotsReq,
		"_secret": secret,
	}

	var reply cluster.ListBotsReply
	if err := gc.SendToWithResponse(target, gossip.UserMsg, req, &reply); err != nil {
		return fmt.Errorf("list bots: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("list bots: %s", reply.Error)
	}

	if len(reply.Bots) == 0 {
		fmt.Println("No bots found.")
		return nil
	}

	nameW := 4
	for _, b := range reply.Bots {
		if len(b.Name) > nameW {
			nameW = len(b.Name)
		}
	}
	for _, b := range reply.Bots {
		running := "○"
		if b.Running {
			running = "●"
		}
		fmt.Printf("%s %-*s  %-10s  %s\n", running, nameW, b.Name, b.Status, b.Goal)
	}
	return nil
}

func remoteLogs(ctx context.Context, cmd *cli.Command, botID string, lines int) error {
	nodeName := cmd.GetString("node")
	if nodeName == "" {
		return fmt.Errorf("--node is required for remote operations")
	}
	seeds := resolveSeeds(cmd)
	if len(seeds) == 0 {
		return fmt.Errorf("--seeds is required for remote operations (or set BOT_SEED_ADDRS)")
	}
	secret := defaultGlobalSecret()

	gc, err := joinCluster(ctx, seeds)
	if err != nil {
		return err
	}
	defer gc.Stop()

	target, err := findWatchdogPeer(ctx, gc, nodeName)
	if err != nil {
		return err
	}

	req := map[string]interface{}{
		"type":    cluster.TypeLogsReq,
		"bot_id":  botID,
		"lines":   lines,
		"_secret": secret,
	}

	var reply cluster.LogsReply
	if err := gc.SendToWithResponse(target, gossip.UserMsg, req, &reply); err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("fetch logs: %s", reply.Error)
	}
	fmt.Print(reply.Content)
	return nil
}

func joinCluster(ctx context.Context, seeds []string) (*gossip.Cluster, error) {
	port := 50000 + rand.N(10000)
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)
	advertiseAddr := fmt.Sprintf("127.0.0.1:%d", port)

	gcfg := gossip.DefaultConfig()
	gcfg.BindAddr = bindAddr
	gcfg.AdvertiseAddr = advertiseAddr
	gcfg.MsgCodec = codec.NewVmihailencoMsgpackCodec()
	gcfg.Transport = gossip.NewSocketTransport(gcfg)

	gc, err := gossip.NewCluster(gcfg)
	if err != nil {
		return nil, fmt.Errorf("create gossip node: %w", err)
	}
	gc.Start()

	gc.LocalMetadata().SetString("id", "operator")
	gc.LocalMetadata().SetString("role", "operator")

	if err := gc.Join(seeds); err != nil {
		gc.Stop()
		return nil, fmt.Errorf("cannot reach cluster via %v: %w", seeds, err)
	}
	return gc, nil
}

func findWatchdogPeer(ctx context.Context, gc *gossip.Cluster, nodeName string) (*gossip.Node, error) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range gc.AliveNodes() {
			if n.Metadata.GetString("role") == "watchdog" && n.Metadata.GetString("node_name") == nodeName {
				return n, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("watchdog node %q not found in cluster", nodeName)
}
