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

	"praxis/internal/bot"
	"praxis/internal/cluster"
)

func spawnCmd() *cli.Command {
	return &cli.Command{
		Name:  "spawn",
		Usage: "Create a new bot",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "name", Usage: "Bot name (letters, digits, dash, underscore)", Required: true},
			&cli.StringArg{Name: "goal", Usage: "Bot goal description", Required: true},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "model", Usage: "LLM model name", EnvVars: []string{"BOT_MODEL"}},
			&cli.StringFlag{Name: "brain", Usage: "Initial brain.md content"},
			&cli.StringFlag{Name: "workspace", Usage: "Workspace name (must exist in praxis.toml)"},
			&cli.StringFlag{Name: "scope", Usage: "Peer visibility: open|isolated|family|gateway"},
			&cli.StringFlag{Name: "allowed-workspaces", Usage: "Comma-separated workspaces for gateway scope"},
			&cli.StringFlag{Name: "parent", Usage: "Parent bot ID (for child bots)"},
			&cli.BoolFlag{Name: "no-thinking", Usage: "Disable thinking mode"},
			&cli.StringFlag{Name: "node", Usage: "Remote watchdog node name to spawn on (default: local)"},
			&cli.StringFlag{Name: "seeds", Usage: "Comma-separated gossip seed addresses (for --node)", EnvVars: []string{"BOT_SEED_ADDRS"}},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			name := cmd.GetStringArg("name")
			goal := cmd.GetStringArg("goal")

			model := cmd.GetString("model")
			if model == "" {
				model = defaultModel()
			}

			cfg := &bot.BotConfig{
				Name:     name,
				Goal:     goal,
				Model:    model,
				Thinking: !cmd.GetBool("no-thinking"),
				Brain:    cmd.GetString("brain"),
				Scope:    cmd.GetString("scope"),
				Parent:   cmd.GetString("parent"),
			}

			if ws := cmd.GetString("workspace"); ws != "" {
				wsPath, wsSecret, wsDefaultScope := resolveWorkspace(app.Dir, ws)
				cfg.Workspace = ws
				cfg.WorkspacePath = wsPath
				if wsSecret != "" {
					cfg.GossipSecret = wsSecret
				}
				if cfg.Scope == "" && wsDefaultScope != "" {
					cfg.Scope = wsDefaultScope
				}
			}

			if aw := cmd.GetString("allowed-workspaces"); aw != "" {
				for _, w := range strings.Split(aw, ",") {
					w = strings.TrimSpace(w)
					if w != "" {
						cfg.AllowedWorkspaces = append(cfg.AllowedWorkspaces, w)
					}
				}
			}

			nodeName := cmd.GetString("node")
			if nodeName != "" {
				return spawnRemoteCLI(ctx, nodeName, cfg, cmd.GetString("seeds"))
			}

			if err := app.Manager.Create(cfg); err != nil {
				return err
			}

			fmt.Printf("spawned %s\n", name)
			fmt.Printf("  dir:   %s\n", app.Manager.BotDir(name))
			fmt.Printf("  goal:  %s\n", goal)
			fmt.Printf("  model: %s\n", model)
			if cfg.Scope != "" {
				fmt.Printf("  scope: %s\n", cfg.Scope)
			}
			if cfg.Workspace != "" {
				fmt.Printf("  workspace: %s\n", cfg.Workspace)
			}
			fmt.Printf("\nstart with: praxis watchdog  (or praxis tui)\n")
			return nil
		},
	}
}

func spawnRemoteCLI(ctx context.Context, nodeName string, cfg *bot.BotConfig, seedsRaw string) error {
	var seeds []string
	for _, a := range strings.Split(seedsRaw, ",") {
		if a = strings.TrimSpace(a); a != "" {
			seeds = append(seeds, a)
		}
	}
	if len(seeds) == 0 {
		return fmt.Errorf("--node requires --seeds to reach the cluster")
	}

	secret := defaultGlobalSecret()

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

	deadline := time.Now().Add(5 * time.Second)
	var target *gossip.Node
	for time.Now().Before(deadline) {
		for _, n := range gc.AliveNodes() {
			if n.Metadata.GetString("role") == "watchdog" && n.Metadata.GetString("node_name") == nodeName {
				target = n
				break
			}
		}
		if target != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if target == nil {
		return fmt.Errorf("node %q not found in cluster", nodeName)
	}

	payload := map[string]interface{}{
		"type":     cluster.TypeRemoteSpawnReq,
		"name":     cfg.Name,
		"goal":     cfg.Goal,
		"model":    cfg.Model,
		"thinking": cfg.Thinking,
	}
	if cfg.Brain != "" {
		payload["brain"] = cfg.Brain
	}
	if cfg.Workspace != "" {
		payload["workspace"] = cfg.Workspace
	}
	if cfg.Scope != "" {
		payload["scope"] = cfg.Scope
	}
	if len(cfg.AllowedWorkspaces) > 0 {
		payload["allowed_workspaces"] = cfg.AllowedWorkspaces
	}
	if secret != "" {
		payload["_secret"] = secret
	}

	var reply cluster.SpawnReply
	if err := gc.SendToWithResponse(target, gossip.UserMsg, payload, &reply); err != nil {
		return fmt.Errorf("remote spawn: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("remote spawn: %s", reply.Error)
	}

	fmt.Printf("spawned %s on %s\n", cfg.Name, nodeName)
	fmt.Printf("  goal:  %s\n", cfg.Goal)
	fmt.Printf("  model: %s\n", cfg.Model)
	return nil
}
