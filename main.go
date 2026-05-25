package main

import (
	"context"
	"fmt"
	"os"

	"praxis/cmd"
)

var version = "0.1.0"

func main() {
	cmd.SetBotcoreTemplate(botcoreTemplate)
	cmd.SetVersion(version)
	if err := cmd.Root().Execute(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
