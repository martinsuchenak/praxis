package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
)

func restartCmd() *cli.Command {
	return &cli.Command{
		Name:  "restart",
		Usage: "Kill and re-queue a bot for start",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
			return restartBot(app, id)
		},
	}
}

func restartStaleCmd() *cli.Command {
	return &cli.Command{
		Name:  "restart-stale",
		Usage: "Restart all bots flagged as stale",
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			bots, err := app.Manager.List()
			if err != nil {
				return err
			}
			threshold := staleThreshold()
			stale := 0
			for _, b := range bots {
				if b.IsStale(threshold) {
					app.Logger.Info("restarting stale bot", "id", b.Config.Name)
					if err := restartBot(app, b.Config.Name); err != nil {
						app.Logger.Error("restart", "bot", b.Config.Name, "err", err)
						continue
					}
					stale++
				}
			}
			if stale == 0 {
				fmt.Println("No stale bots found.")
			} else {
				fmt.Printf("done. restarted %d stale bots\n", stale)
			}
			return nil
		},
	}
}

func restartBot(app *AppContext, id string) error {
	if err := killBot(app, id); err != nil {
		return err
	}
	time.Sleep(500 * time.Millisecond)
	if err := app.Manager.SetStatus(id, bot.StatusCreated); err != nil {
		return err
	}
	fmt.Printf("restarted %s\n", id)
	warnIfNoWatchdog(app)
	return nil
}
