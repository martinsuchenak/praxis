package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/paularlott/cli"
)

func statusCmd() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "Show swarm status (file-based snapshot)",
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)

			bots, err := app.Manager.List()
			if err != nil {
				return err
			}

			if len(bots) == 0 {
				fmt.Println("no bots found")
				return nil
			}

			fmt.Printf("%-20s %-10s %-8s %s\n", "NAME", "STATUS", "SCOPE", "LAST TICK")
			fmt.Printf("%-20s %-10s %-8s %s\n",
				"--------------------", "----------", "--------", "---------")

			for _, b := range bots {
				lastTick := "-"
				if b.State.LastTickTS > 0 {
					d := time.Since(time.Unix(b.State.LastTickTS, 0)).Round(time.Second)
					lastTick = d.String() + " ago"
				}
				scope := b.Config.Scope
				if scope == "" {
					scope = "open"
				}
				fmt.Printf("%-20s %-10s %-8s %s\n",
					b.Config.Name,
					b.State.Status,
					scope,
					lastTick,
				)
			}
			return nil
		},
	}
}
