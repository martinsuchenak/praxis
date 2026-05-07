package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
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
			log.Info("sandbox ready", "type", sb.Name())

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

			log.Info("watchdog started",
				"port", cfg.Watchdog.Port,
				"advertise", cfg.ClusterAdvertiseAddr(),
				"dir", app.Dir,
			)

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

			go monitorBotStates(runCtx, app.Manager, pool, log)

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
