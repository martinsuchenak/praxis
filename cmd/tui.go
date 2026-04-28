package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
	"praxis/internal/cluster"
	"praxis/internal/sandbox"
	"praxis/internal/tui"
)

func tuiCmd() *cli.Command {
	return &cli.Command{
		Name:  "tui",
		Usage: "Start the interactive TUI dashboard (includes watchdog)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:         "port",
				Usage:        "Gossip listen port",
				DefaultValue: "7700",
				EnvVars:      []string{"BOT_WATCHDOG_PORT"},
			},
			&cli.StringFlag{
				Name:    "advertise",
				Usage:   "Gossip advertise address (default: 0.0.0.0:<port>)",
				EnvVars: []string{"BOT_WATCHDOG_ADDR"},
			},
			&cli.StringFlag{
				Name:    "seeds",
				Usage:   "Comma-separated seed peer addresses",
				EnvVars: []string{"BOT_SEED_ADDRS"},
			},
			&cli.StringFlag{
				Name:    "secret",
				Usage:   "Global gossip secret",
				EnvVars: []string{"BOT_GLOBAL_SECRET"},
			},
			&cli.StringFlag{
				Name:         "sandbox",
				Usage:        "Sandbox mode: auto|bwrap|none",
				DefaultValue: "auto",
				EnvVars:      []string{"BOT_SHELL_SANDBOX"},
			},
			&cli.StringFlag{
				Name:    "mounts",
				Usage:   "Extra sandbox mounts",
				EnvVars: []string{"BOT_SHELL_MOUNTS"},
			},
			&cli.StringFlag{
				Name:    "allowlist",
				Usage:   "Comma-separated shell command allowlist (empty = all allowed)",
				EnvVars: []string{"BOT_SHELL_ALLOWLIST"},
			},
			&cli.BoolFlag{
				Name:    "auth-disabled",
				Usage:   "Disable gossip secret validation (dev mode)",
				EnvVars: []string{"BOT_AUTH_DISABLED"},
			},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			log := app.Logger

			port := cmd.GetString("port")
			advertise := cmd.GetString("advertise")
			if advertise == "" {
				advertise = "0.0.0.0:" + port
			}

			sbCfg := sandbox.Config{
				Mode:        sandbox.SandboxMode(cmd.GetString("sandbox")),
				ExtraMounts: cmd.GetString("mounts"),
			}
			sb, warn, err := sandbox.New(sbCfg)
			if err != nil {
				return fmt.Errorf("sandbox: %w", err)
			}
			if warn != "" {
				log.Warn(warn)
			}

			var seeds []string
			if s := cmd.GetString("seeds"); s != "" {
				for _, addr := range strings.Split(s, ",") {
					addr = strings.TrimSpace(addr)
					if addr != "" {
						seeds = append(seeds, addr)
					}
				}
			}

			clusterCfg := cluster.Config{
				BindAddr:       "0.0.0.0:" + port,
				AdvertiseAddr:  advertise,
				Seeds:          seeds,
				GlobalSecret:   cmd.GetString("secret"),
				ExtraMounts:    cmd.GetString("mounts"),
				ShellAllowlist: parseCSVFlag(cmd.GetString("allowlist")),
				AuthDisabled:   cmd.GetBool("auth-disabled"),
			}

			node, err := cluster.New(clusterCfg, app.Manager, sb, log)
			if err != nil {
				return fmt.Errorf("cluster: %w", err)
			}

			runnerCfg := bot.RunnerConfig{
				WatchdogAddr: advertise,
			}
			pool := bot.NewRunnerPool(app.Manager, runnerCfg, log)

			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			if err := node.Start(runCtx); err != nil {
				return fmt.Errorf("cluster start: %w", err)
			}

			// Auto-start bots from a previous run.
			if bots, err := app.Manager.List(); err == nil {
				for _, b := range bots {
					switch b.State.Status {
					case bot.StatusCreated, bot.StatusRunning, bot.StatusStarting:
						_ = pool.Start(b.Config.Name)
					}
				}
			}

			go monitorBotStates(runCtx, app.Manager, pool, log)

			dashboard := tui.New(app.Manager, pool, node, sb, log)
			if err := dashboard.Run(runCtx); err != nil {
				return fmt.Errorf("tui: %w", err)
			}

			cancel()
			pool.KillAll()
			node.Stop()
			return nil
		},
	}
}
