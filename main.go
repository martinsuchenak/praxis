package main

import (
	"context"
	"fmt"
	"os"

	"praxis/cmd"
)

func main() {
	cmd.SetBotcoreTemplate(botcoreTemplate)
	if err := cmd.Root().Execute(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
