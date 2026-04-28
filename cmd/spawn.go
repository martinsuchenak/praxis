package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
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
			&cli.StringFlag{Name: "workspace", Usage: "Workspace name (must exist in workspaces.json)"},
			&cli.StringFlag{Name: "scope", Usage: "Peer visibility: open|isolated|family|gateway"},
			&cli.StringFlag{Name: "allowed-workspaces", Usage: "Comma-separated workspaces for gateway scope"},
			&cli.StringFlag{Name: "parent", Usage: "Parent bot ID (for child bots)"},
			&cli.BoolFlag{Name: "no-thinking", Usage: "Disable thinking mode"},
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
