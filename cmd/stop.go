package cmd

import (
	"context"
	"fmt"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
)

func stopCmd() *cli.Command {
	return &cli.Command{
		Name:  "stop",
		Usage: "Gracefully stop a bot (signals on next tick)",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
			if _, err := app.Manager.Get(id); err != nil {
				return err
			}
			if err := app.Manager.SetStatus(id, bot.StatusStopping); err != nil {
				return err
			}
			fmt.Printf("stop signal sent to %s (exits on next tick)\n", id)
			return nil
		},
	}
}

func stopAllCmd() *cli.Command {
	return &cli.Command{
		Name:  "stop-all",
		Usage: "Gracefully stop all running bots",
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			bots, err := app.Manager.List()
			if err != nil {
				return err
			}
			stopped := 0
			for _, b := range bots {
				if b.State.Status == bot.StatusRunning {
					if err := app.Manager.SetStatus(b.Config.Name, bot.StatusStopping); err != nil {
						app.Logger.Error("set status", "bot", b.Config.Name, "err", err)
						continue
					}
					stopped++
				}
			}
			fmt.Printf("done. stop signals sent: %d\n", stopped)
			return nil
		},
	}
}
