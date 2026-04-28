package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/paularlott/cli"
)

var validLogs = map[string]bool{
	"bot.log":    true,
	"output.log": true,
}

func logsCmd() *cli.Command {
	return &cli.Command{
		Name:  "logs",
		Usage: "Show recent log lines for a bot",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Flags: []cli.Flag{
			&cli.IntFlag{Name: "lines", Usage: "Number of lines", DefaultValue: 40},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
			if _, err := app.Manager.Get(id); err != nil {
				return err
			}
			n := cmd.GetInt("lines")
			botDir := app.Manager.BotDir(id)
			for _, logName := range []string{"bot.log", "output.log"} {
				logPath := filepath.Join(botDir, logName)
				fmt.Printf("--- %s (last %d lines) ---\n", logName, n)
				if err := printLastN(logPath, n); err != nil {
					fmt.Println("(empty)")
				}
			}
			return nil
		},
	}
}

func tailCmd() *cli.Command {
	return &cli.Command{
		Name:  "tail",
		Usage: "Follow a bot log in real time",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:         "log",
				Usage:        "Log file: bot or output (default: bot)",
				DefaultValue: "bot",
			},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			id := cmd.GetStringArg("bot")
			if _, err := app.Manager.Get(id); err != nil {
				return err
			}
			logName := cmd.GetString("log") + ".log"
			if !validLogs[logName] {
				return fmt.Errorf("unknown log %q — use bot or output", cmd.GetString("log"))
			}
			logPath := filepath.Join(app.Manager.BotDir(id), logName)
			return tailFollow(ctx, logPath)
		},
	}
}

func printLastN(path string, n int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	// collect all lines, keep last n
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		fmt.Println(l)
	}
	return scanner.Err()
}

func tailFollow(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("log not found: %s", path)
	}
	defer f.Close() //nolint:errcheck

	// seek to end
	offset, _ := f.Seek(0, io.SeekEnd)
	fmt.Printf("[following %s — Ctrl+C to stop]\n", path)

	buf := make([]byte, 32*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		n, err := f.ReadAt(buf, offset)
		if n > 0 {
			fmt.Print(string(buf[:n]))
			offset += int64(n)
		}
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
}

