package cmd

import (
	"context"
	"fmt"

	"github.com/paularlott/cli"

	"praxis/internal/config"
)

func initCmd() *cli.Command {
	return &cli.Command{
		Name:        "init",
		Usage:       "Initialize praxis configuration",
		Description: "Creates a praxis.toml config file. With a path argument, creates a project-level config. Without, creates the global config.",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "path", Usage: "Project directory (default: global config dir)", Required: false},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			target := cmd.GetStringArg("path")

			dir, err := config.InitDir(target)
			if err != nil {
				return err
			}

			if target == "" {
				fmt.Printf("created global config: %s/config.toml\n", dir)
			} else {
				fmt.Printf("created project config: %s/config.toml\n", dir)
				fmt.Printf("\nedit it with your API key and model settings, then:\n")
				fmt.Printf("  cd %s && praxis tui\n", dir)
			}
			return nil
		},
	}
}
