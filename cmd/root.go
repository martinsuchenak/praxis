package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/paularlott/cli"
	"github.com/paularlott/logger"
	logslog "github.com/paularlott/logger/slog"

	"praxis/internal/bot"
	"praxis/internal/config"
)

type contextKey string

const appKey contextKey = "app"

type AppContext struct {
	Dir     string
	Cfg     *config.Config
	Logger  logger.Logger
	Manager *bot.Manager
}

func appCtx(ctx context.Context) *AppContext {
	return ctx.Value(appKey).(*AppContext)
}

var botcoreTemplate []byte

func SetBotcoreTemplate(b []byte) { botcoreTemplate = b }

func Root() *cli.Command {
	var dir string

	return &cli.Command{
		Name:        "praxis",
		Version:     "0.1.0",
		Usage:       "Praxis bot swarm controller",
		Description: "Manages a swarm of autonomous scriptling bots.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:         "dir",
				Usage:        "Praxis project directory",
				DefaultValue: ".",
				EnvVars:      []string{"PRAXIS_DIR"},
				AssignTo:     &dir,
			},
			&cli.StringFlag{
				Name:         "log-level",
				Usage:        "Log level (trace, debug, info, warn, error)",
				DefaultValue: "info",
				EnvVars:      []string{"LOG_LEVEL"},
			},
			&cli.StringFlag{
				Name:         "log-format",
				Usage:        "Log format (console, json)",
				DefaultValue: "console",
				EnvVars:      []string{"LOG_FORMAT"},
			},
		},
		PreRun: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			abs, err := filepath.Abs(dir)
			if err != nil {
				abs = dir
			}

			cfg, err := config.Load(abs)
			if err != nil {
				return nil, err
			}

			log := logslog.New(logslog.Config{
				Level:  cmd.GetString("log-level"),
				Format: cmd.GetString("log-format"),
				Writer: os.Stderr,
			})

			mgr := bot.NewManager(abs)
			mgr.TemplateBytes = botcoreTemplate
			app := &AppContext{
				Dir:     abs,
				Cfg:     cfg,
				Logger:  log,
				Manager: mgr,
			}
			return context.WithValue(ctx, appKey, app), nil
		},
		Commands: []*cli.Command{
			initCmd(),
			spawnCmd(),
			listCmd(),
			startCmd(),
			startAllCmd(),
			stopCmd(),
			stopAllCmd(),
			killCmd(),
			killAllCmd(),
			restartCmd(),
			restartStaleCmd(),
			logsCmd(),
			tailCmd(),
			removeCmd(),
			sendCmd(),
			statusCmd(),
			exportCmd(),
			importCmd(),
			watchdogCmd(),
			tuiCmd(),
		},
	}
}
