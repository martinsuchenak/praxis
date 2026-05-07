package cmd

import (
	"context"
	"fmt"

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
		Flags: watchdogFlags(),
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			log := app.Logger
			cfg := overlayWatchdogFlags(app.Cfg, cmd)

			sbCfg := sandbox.Config{
				Mode:        sandbox.SandboxMode(cfg.Watchdog.Sandbox),
				ExtraMounts: cfg.Watchdog.Mounts,
			}
			sb, warn, err := sandbox.New(sbCfg)
			if err != nil {
				return fmt.Errorf("sandbox: %w", err)
			}
			if warn != "" {
				log.Warn(warn)
			}

			clusterCfg := cluster.Config{
				BindAddr:        cfg.ClusterBindAddr(),
				AdvertiseAddr:   cfg.ClusterAdvertiseAddr(),
				Seeds:           cfg.Watchdog.Seeds,
				GlobalSecret:    cfg.Watchdog.Secret,
				ExtraMounts:     cfg.Watchdog.Mounts,
				ShellAllowlist:  cfg.Watchdog.Allowlist,
				AuthDisabled:    cfg.Watchdog.AuthDisabled,
				NodeName:        cfg.Watchdog.NodeName,
				MulticastAddr:   cfg.Watchdog.MulticastAddr,
				MulticastPort:   cfg.Watchdog.MulticastPort,
				TsnetHostname:   cfg.Tsnet.Hostname,
				TsnetDir:        cfg.TsnetDirOrDefault(app.Dir),
				TsnetAuthKey:    cfg.Tsnet.AuthKey,
				TsnetControlURL: cfg.Tsnet.ControlURL,
			}

			node, err := cluster.New(clusterCfg, app.Manager, sb, log)
			if err != nil {
				return fmt.Errorf("cluster: %w", err)
			}

			runnerCfg := bot.RunnerConfig{
				WatchdogAddr: cfg.ClusterAdvertiseAddr(),
				ModelsDir:    cfg.ModelsDirResolved(app.Dir),
			}
			pool := bot.NewRunnerPool(app.Manager, runnerCfg, log)

			runCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			if err := node.Start(runCtx); err != nil {
				return fmt.Errorf("cluster start: %w", err)
			}

			if bots, err := app.Manager.List(); err == nil {
				for _, b := range bots {
					switch b.State.Status {
					case bot.StatusCreated, bot.StatusRunning, bot.StatusStarting:
						_ = pool.Start(b.Config.Name)
					}
				}
			}

			go monitorBotStates(runCtx, app.Manager, pool, log)

			dashboard := tui.New(app.Manager, pool, node, sb, log, cfg)
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
