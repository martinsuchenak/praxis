package cmd

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
)

func listCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "List all bots",
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			bots, err := app.Manager.List()
			if err != nil {
				return err
			}
			if len(bots) == 0 {
				fmt.Println("No bots found.")
				return nil
			}

			threshold := staleThreshold()
			sort.Slice(bots, func(i, j int) bool {
				return bots[i].Config.Name < bots[j].Config.Name
			})

			nameW := 4
			for _, b := range bots {
				if len(b.Config.Name) > nameW {
					nameW = len(b.Config.Name)
				}
			}

			for _, b := range bots {
				statusStr := displayStatus(b, threshold)
				line := fmt.Sprintf("%-*s  %-10s  ticks=%-6d spawns=%d",
					nameW, b.Config.Name,
					statusStr,
					b.State.TicksAlive(),
					b.State.Spawns(),
				)
				if b.State.GossipAddr != "" {
					line += "  @ " + b.State.GossipAddr
				}
				fmt.Println(line)
				fmt.Printf("  %s\n", b.Config.Goal)
				if b.State.IsLeader {
					fmt.Println("  ** leader **")
				}
			}
			return nil
		},
	}
}

func displayStatus(b *bot.Bot, threshold time.Duration) string {
	if b.IsStale(threshold) {
		return "STALE"
	}
	return b.State.Status
}

func staleThreshold() time.Duration {
	return time.Duration(envInt("BOT_STALE_THRESHOLD", 120)) * time.Second
}
