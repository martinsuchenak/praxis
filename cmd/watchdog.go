package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
	"praxis/internal/cluster"
	"praxis/internal/sandbox"
)

func watchdogCmd() *cli.Command {
	return &cli.Command{
		Name:  "watchdog",
		Usage: "Start the gossip node, bot runner pool, and crash recovery (headless)",
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
				Usage:   "Global gossip secret (fallback when no workspace secret)",
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
				Usage:   "Extra sandbox mounts (BOT_SHELL_MOUNTS format)",
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

			// Build sandbox.
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
			log.Info("sandbox ready", "type", sb.Name())

			// Build cluster config.
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

			// Create runner pool with watchdog as the gossip seed for bots.
			runnerCfg := bot.RunnerConfig{
				WatchdogAddr: advertise,
			}
			pool := bot.NewRunnerPool(app.Manager, runnerCfg, log)

			// Create a cancellable context for the watchdog lifetime.
			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			if err := node.Start(runCtx); err != nil {
				return fmt.Errorf("cluster start: %w", err)
			}

			log.Info("watchdog started",
				"port", port,
				"advertise", advertise,
				"dir", app.Dir,
			)

			// Auto-start bots in created/running state from a previous run.
			if bots, err := app.Manager.List(); err == nil {
				for _, b := range bots {
					switch b.State.Status {
					case bot.StatusCreated, bot.StatusRunning, bot.StatusStarting:
						if startErr := pool.Start(b.Config.Name); startErr != nil {
							log.Error("auto-start failed", "bot", b.Config.Name, "err", startErr)
						} else {
							log.Info("auto-started bot", "bot", b.Config.Name)
						}
					}
				}
			}

			// Monitor loop: watch for one-shot command signals in state.json.
			go monitorBotStates(runCtx, app.Manager, pool, log)

			// Wait for SIGINT / SIGTERM.
			quit := make(chan os.Signal, 1)
			signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
			select {
			case sig := <-quit:
				log.Info("received signal, shutting down", "signal", sig)
			case <-ctx.Done():
			}

			cancel()
			pool.KillAll()
			node.Stop()
			log.Info("watchdog stopped")
			return nil
		},
	}
}

// monitorBotStates polls bots' state.json files and reacts to file-based
// signals written by one-shot CLI commands (start, kill).
func monitorBotStates(ctx context.Context, mgr *bot.Manager, pool *bot.RunnerPool, log interface {
	Info(string, ...interface{})
	Error(string, ...interface{})
}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bots, err := mgr.List()
			if err != nil {
				continue
			}
			for _, b := range bots {
				name := b.Config.Name
				switch b.State.Status {
				case bot.StatusCreated:
					if !pool.IsRunning(name) {
						if err := pool.Start(name); err != nil {
							log.Error("monitor: start failed", "bot", name, "err", err)
						} else {
							log.Info("monitor: started bot", "bot", name)
						}
					}
				case bot.StatusKilled:
					if pool.IsRunning(name) {
						if err := pool.Kill(name); err != nil {
							log.Error("monitor: kill failed", "bot", name, "err", err)
						}
					}
				}
			}
		}
	}
}
