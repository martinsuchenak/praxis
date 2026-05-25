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
		Flags: remoteFlags(),
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")

			if nodeName := cmd.GetString("node"); nodeName != "" {
				if err := remoteControlBot(ctx, cmd, id, "kill"); err != nil {
					return err
				}
				fmt.Printf("killed %s on %s\n", id, nodeName)
				return nil
			}

			return killBot(app, id)
		},
	}
}

func killAllCmd() *cli.Command {
	return &cli.Command{
		Name:  "kill-all",
		Usage: "Immediately terminate all bot processes",
		Flags: remoteFlags(),
		Run: func(ctx context.Context, cmd *cli.Command) error {
			if nodeName := cmd.GetString("node"); nodeName != "" {
				acted, err := remoteControlAll(ctx, cmd, "kill")
				if err != nil {
					return err
				}
				fmt.Printf("done. killed=%d on %s\n", acted, nodeName)
				return nil
			}

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
