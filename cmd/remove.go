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
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
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
