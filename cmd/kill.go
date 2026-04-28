package cmd

import (
	"context"
	"fmt"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
)

func killCmd() *cli.Command {
	return &cli.Command{
		Name:  "kill",
		Usage: "Immediately terminate a bot process",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
			return killBot(app, id)
		},
	}
}

func killAllCmd() *cli.Command {
	return &cli.Command{
		Name:  "kill-all",
		Usage: "Immediately terminate all bot processes",
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			bots, err := app.Manager.List()
			if err != nil {
				return err
			}
			killed := 0
			for _, b := range bots {
				if err := killBot(app, b.Config.Name); err != nil {
					app.Logger.Error("kill", "bot", b.Config.Name, "err", err)
					continue
				}
				killed++
			}
			fmt.Printf("done. killed=%d\n", killed)
			return nil
		},
	}
}

func killBot(app *AppContext, id string) error {
	if _, err := app.Manager.Get(id); err != nil {
		return err
	}
	if err := app.Manager.SetStatus(id, bot.StatusKilled); err != nil {
		return err
	}
	fmt.Printf("killed %s\n", id)
	return nil
}
