package cmd

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/paularlott/cli"
	"github.com/paularlott/gossip"
	"github.com/paularlott/gossip/codec"
)

func sendCmd() *cli.Command {
	return &cli.Command{
		Name:  "send",
		Usage: "Send a message to a bot via gossip",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
			&cli.StringArg{Name: "message", Usage: "Message text", Required: true},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "seeds",
				Usage:   "Comma-separated gossip seed addresses to reach the cluster",
				EnvVars: []string{"BOT_SEED_ADDRS"},
			},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			botName := cmd.GetStringArg("bot")
			message := cmd.GetStringArg("message")

			b, err := app.Manager.Get(botName)
			if err != nil {
				return fmt.Errorf("bot %q not found", botName)
			}

			// Build seed list: explicit --seeds flag, then env BOT_SEED_ADDRS,
			// then fall back to the bot's last known gossip address.
			var seeds []string
			if s := cmd.GetString("seeds"); s != "" {
				for _, a := range strings.Split(s, ",") {
					if a = strings.TrimSpace(a); a != "" {
						seeds = append(seeds, a)
					}
				}
			}
			if len(seeds) == 0 && b.State.GossipAddr != "" {
				seeds = append(seeds, b.State.GossipAddr)
			}
			if len(seeds) == 0 {
				return fmt.Errorf("no gossip address available — provide --seeds or ensure the bot has run at least once")
			}

			secret := b.Config.GossipSecret
			if secret == "" {
				secret = defaultGlobalSecret()
			}

			return sendGossipMessage(ctx, botName, message, secret, seeds)
		},
	}
}

// sendGossipMessage creates a short-lived gossip node, joins via seeds, finds
// the named bot, sends a "message" payload, then shuts down.
func sendGossipMessage(ctx context.Context, botName, content, secret string, seeds []string) error {
	port := 50000 + rand.N(10000)
	bindAddr := fmt.Sprintf("0.0.0.0:%d", port)

	gcfg := gossip.DefaultConfig()
	gcfg.BindAddr = bindAddr
	gcfg.AdvertiseAddr = bindAddr
	gcfg.MsgCodec = codec.NewVmihailencoMsgpackCodec()
	gcfg.Transport = gossip.NewSocketTransport(gcfg)

	gc, err := gossip.NewCluster(gcfg)
	if err != nil {
		return fmt.Errorf("create gossip node: %w", err)
	}
	gc.Start()
	defer gc.Stop()

	gc.LocalMetadata().SetString("id", "operator")

	if err := gc.Join(seeds); err != nil {
		return fmt.Errorf("cannot reach cluster via %v: %w", seeds, err)
	}

	// Wait up to 3s for the target bot to appear as a live peer.
	deadline := time.Now().Add(3 * time.Second)
	var botNode *gossip.Node
	for time.Now().Before(deadline) {
		for _, n := range gc.AliveNodes() {
			if n.Metadata.GetString("id") == botName {
				botNode = n
				break
			}
		}
		if botNode != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if botNode == nil {
		return fmt.Errorf("bot %q not found in cluster (not alive)", botName)
	}

	payload := map[string]interface{}{
		"type":    "message",
		"content": content,
	}
	if secret != "" {
		payload["_secret"] = secret
	}

	if err := gc.SendToReliable(botNode, gossip.UserMsg, payload); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	fmt.Printf("message sent to %s\n", botName)
	return nil
}
