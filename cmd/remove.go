package cmd

import (
	"context"
	"fmt"

	"github.com/paularlott/cli"
)

func removeCmd() *cli.Command {
	return &cli.Command{
		Name:  "remove",
		Usage: "Kill and permanently delete a bot",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Flags: remoteFlags(),
		Run: func(ctx context.Context, cmd *cli.Command) error {
			id := cmd.GetStringArg("bot")

			if nodeName := cmd.GetString("node"); nodeName != "" {
				if err := remoteControlBot(ctx, cmd, id, "remove"); err != nil {
					return err
				}
				fmt.Printf("removed %s on %s\n", id, nodeName)
				return nil
			}

			app := appCtx(ctx)
			if _, err := app.Manager.Get(id); err != nil {
				return err
			}
			_ = killBot(app, id)

			if err := app.Manager.Delete(id); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", id)
			return nil
		},
	}
}
