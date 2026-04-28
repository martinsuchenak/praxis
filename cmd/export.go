package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"archive/tar"
	"compress/gzip"

	"github.com/paularlott/cli"

	"praxis/internal/bot"
)

func exportCmd() *cli.Command {
	return &cli.Command{
		Name:  "export",
		Usage: "Package a bot into a portable archive",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "bot", Usage: "Bot name", Required: true},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "output",
				Aliases: []string{"o"},
				Usage:   "Output archive path (default: <bot>.tar.gz)",
			},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			name := cmd.GetStringArg("bot")

			b, err := app.Manager.Get(name)
			if err != nil {
				return fmt.Errorf("bot not found: %w", err)
			}

			outPath := cmd.GetString("output")
			if outPath == "" {
				outPath = name + ".tar.gz"
			}

			if err := bot.Export(b, outPath); err != nil {
				return fmt.Errorf("export: %w", err)
			}

			fmt.Printf("exported %s → %s\n", name, outPath)
			return nil
		},
	}
}

func importCmd() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Import a bot archive and remap workspace paths",
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "archive", Usage: "Path to archive file", Required: true},
		},
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "workspace",
				Usage: "Workspace mapping: name=/local/path (repeatable)",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Override the bot name on import",
			},
		},
		Run: func(ctx context.Context, cmd *cli.Command) error {
			app := appCtx(ctx)
			archivePath := cmd.GetStringArg("archive")
			nameOverride := cmd.GetString("name")

			wsMappings := parseWorkspaceMappings(cmd.GetString("workspace"))

			name, err := importBot(archivePath, app.Manager.BotsDir, nameOverride, wsMappings)
			if err != nil {
				return fmt.Errorf("import: %w", err)
			}

			fmt.Printf("imported %s into %s\n", name, filepath.Join(app.Manager.BotsDir, name))
			return nil
		},
	}
}

func importBot(archivePath, botsDir, nameOverride string, wsMappings map[string]string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	type entry struct {
		header *tar.Header
		data   []byte
	}
	var botEntries []entry
	var configData []byte

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read archive: %w", err)
		}

		data, err := io.ReadAll(tr)
		if err != nil {
			return "", fmt.Errorf("read entry %s: %w", hdr.Name, err)
		}

		if !strings.HasPrefix(hdr.Name, "bot/") {
			continue
		}

		botEntries = append(botEntries, entry{hdr, data})
		if hdr.Name == "bot/config.json" {
			configData = data
		}
	}

	if configData == nil {
		return "", fmt.Errorf("archive missing bot/config.json")
	}

	var cfg bot.BotConfig
	if err := json.Unmarshal(configData, &cfg); err != nil {
		return "", fmt.Errorf("parse config: %w", err)
	}

	botName := cfg.Name
	if nameOverride != "" {
		botName = nameOverride
		cfg.Name = nameOverride
	}

	if newPath, ok := wsMappings[cfg.Workspace]; ok {
		cfg.WorkspacePath = newPath
	}

	botDir := filepath.Join(botsDir, botName)
	if _, err := os.Stat(botDir); err == nil {
		return "", fmt.Errorf("bot already exists: %s", botName)
	}
	if err := os.MkdirAll(botDir, 0o755); err != nil {
		return "", fmt.Errorf("create bot dir: %w", err)
	}

	for _, e := range botEntries {
		rel := strings.TrimPrefix(e.header.Name, "bot/")
		if rel == "" {
			continue
		}
		dst := filepath.Join(botDir, rel)

		if e.header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return "", fmt.Errorf("mkdir %s: %w", dst, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("mkdir parent: %w", err)
		}

		if rel == "config.json" {
			continue
		}

		if err := os.WriteFile(dst, e.data, os.FileMode(e.header.Mode)); err != nil {
			return "", fmt.Errorf("write %s: %w", dst, err)
		}
	}

	cfgData, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	cfgPath := filepath.Join(botDir, "config.json")
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, cfgData, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		return "", err
	}

	return botName, nil
}

func parseWorkspaceMappings(s string) map[string]string {
	m := make(map[string]string)
	if s == "" {
		return m
	}
	for _, part := range strings.Split(s, ",") {
		if idx := strings.IndexByte(part, '='); idx > 0 {
			m[strings.TrimSpace(part[:idx])] = strings.TrimSpace(part[idx+1:])
		}
	}
	return m
}
