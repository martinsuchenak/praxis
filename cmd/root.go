package cmd

import (
	"context"
	"os"
	"path/filepath"

	"github.com/paularlott/cli"
	clienv "github.com/paularlott/cli/env"
	"github.com/paularlott/logger"
	logslog "github.com/paularlott/logger/slog"

	"praxis/internal/bot"
)

type contextKey string

const appKey contextKey = "app"

// AppContext holds shared state passed to all subcommands via context.
type AppContext struct {
	Dir     string
	Logger  logger.Logger
	Manager *bot.Manager
}

func appCtx(ctx context.Context) *AppContext {
	return ctx.Value(appKey).(*AppContext)
}

// botcoreTemplate is set by main via SetBotcoreTemplate before Root() is called.
var botcoreTemplate []byte

// SetBotcoreTemplate provides the embedded botcore.py bytes to the cmd package.
// Must be called before Root().Execute().
func SetBotcoreTemplate(b []byte) { botcoreTemplate = b }

// Root builds and returns the root CLI command.
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
			// Load .env from the project directory
			abs, err := filepath.Abs(dir)
			if err != nil {
				abs = dir
			}
			envFile := filepath.Join(abs, ".env")
			if _, err := os.Stat(envFile); err == nil {
				_ = clienv.Load(envFile)
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
				Logger:  log,
				Manager: mgr,
			}
			return context.WithValue(ctx, appKey, app), nil
		},
		Commands: []*cli.Command{
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

