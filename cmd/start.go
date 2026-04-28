package cmd

import (
	"context"
	"fmt"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
)

func startCmd() *cli.Command {
	return &cli.Command{
		Name:  "start",
		Usage: "Signal a bot to start (watchdog picks it up)",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
			b, err := app.Manager.Get(id)
			if err != nil {
				return err
			}
			switch b.State.Status {
			case bot.StatusRunning, bot.StatusStarting:
				fmt.Printf("%s is already %s\n", id, b.State.Status)
				return nil
			}
			if err := app.Manager.SetStatus(id, bot.StatusCreated); err != nil {
				return err
			}
			fmt.Printf("start signal sent to %s — watchdog will pick it up\n", id)
			warnIfNoWatchdog(app)
			return nil
		},
	}
}

func startAllCmd() *cli.Command {
	return &cli.Command{
		Name:  "start-all",
		Usage: "Signal all stopped bots to start",
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			bots, err := app.Manager.List()
			if err != nil {
				return err
			}
			started := 0
			skipped := 0
			for _, b := range bots {
				switch b.State.Status {
				case bot.StatusRunning, bot.StatusStarting:
					skipped++
					continue
				}
				if err := app.Manager.SetStatus(b.Config.Name, bot.StatusCreated); err != nil {
					app.Logger.Error("set status", "bot", b.Config.Name, "err", err)
					continue
				}
				started++
			}
			fmt.Printf("done. started=%d skipped=%d\n", started, skipped)
			if started > 0 {
				warnIfNoWatchdog(app)
			}
			return nil
		},
	}
}

func warnIfNoWatchdog(app *AppContext) {
	// Simple heuristic: check if any bot is running and has a gossip addr
	// that would indicate the watchdog is reachable. For now just print advice.
	fmt.Println("  tip: praxis watchdog (or tui) must be running for bots to function")
}
