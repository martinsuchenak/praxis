package bot

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func Export(b *Bot, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	if err := addDirToTar(tw, b.Dir, "bot"); err != nil {
		return fmt.Errorf("add bot dir: %w", err)
	}

	exe, err := os.Executable()
	if err == nil {
		if err := addFileToTar(tw, exe, "praxis", 0o755); err != nil {
			return fmt.Errorf("add binary: %w", err)
		}
	}

	tomlExample := buildPraxisTomlExample(b.Config)
	if err := addBytesToTar(tw, []byte(tomlExample), "praxis.example.toml", 0o644); err != nil {
		return fmt.Errorf("add config example: %w", err)
	}

	bootstrap := buildBootstrap(b.Config.Name)
	if err := addBytesToTar(tw, []byte(bootstrap), "bootstrap.sh", 0o755); err != nil {
		return fmt.Errorf("add bootstrap: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar: %w", err)
	}
	return gw.Close()
}

func buildPraxisTomlExample(cfg *BotConfig) string {
	out := `# Praxis Configuration
# Copy to praxis.toml and fill in values

[watchdog]
port = "7700"
sandbox = "auto"

[bot]
base_url = ""
model = ""
api_key = ""
tick_interval = 30
tick_max_iterations = 5
`
	if cfg.Workspace != "" {
		out += fmt.Sprintf("\n[[workspace]]\nname = %q\npath = \"\"\n", cfg.Workspace)
	}
	return out
}

func buildBootstrap(botName string) string {
	return fmt.Sprintf(`#!/bin/sh
# Bootstrap script for bot %q
set -e
cp praxis.example.toml praxis.toml
echo "Edit praxis.toml with your API key and settings"
./praxis watchdog &
echo "watchdog started"
./praxis start %s
echo "bot %s started"
`, botName, botName, botName)
}

func addDirToTar(tw *tar.Writer, srcDir, archivePrefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		name := archivePrefix + "/" + filepath.ToSlash(rel)

		if info.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Typeflag: tar.TypeDir,
				Name:     name + "/",
				Mode:     int64(info.Mode()),
				ModTime:  info.ModTime(),
			})
		}

		return addFileToTar(tw, path, name, info.Mode())
	})
}

func addFileToTar(tw *tar.Writer, src, archiveName string, mode os.FileMode) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     archiveName,
		Size:     info.Size(),
		Mode:     int64(mode),
		ModTime:  info.ModTime(),
	}); err != nil {
		return err
	}

	_, err = io.Copy(tw, f)
	return err
}

func addBytesToTar(tw *tar.Writer, data []byte, archiveName string, mode os.FileMode) error {
	if err := tw.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg,
		Name:     archiveName,
		Size:     int64(len(data)),
		Mode:     int64(mode),
		ModTime:  time.Now(),
	}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
