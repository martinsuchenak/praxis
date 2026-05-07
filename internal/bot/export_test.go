package bot_test

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"praxis/internal/bot"
	"praxis/internal/testutil"
)

func TestExportCreatesArchive(t *testing.T) {
	root := testutil.TempProject(t)
	mgr := bot.NewManager(root)
	cfg := &bot.BotConfig{Name: "exportbot", Goal: "test export", Model: "test-model"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatal(err)
	}

	b, err := mgr.Get("exportbot")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "exportbot.tar.gz")
	if err := bot.Export(b, outPath); err != nil {
		t.Fatalf("Export: %v", err)
	}

	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("archive not created: %v", err)
	}
}

func TestExportContainsBotDir(t *testing.T) {
	root := testutil.TempProject(t)
	mgr := bot.NewManager(root)
	cfg := &bot.BotConfig{Name: "packbot", Goal: "pack test", Model: "test-model"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatal(err)
	}

	b, err := mgr.Get("packbot")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "packbot.tar.gz")
	if err := bot.Export(b, outPath); err != nil {
		t.Fatal(err)
	}

	entries := listTarGz(t, outPath)
	hasConfig := false
	for _, name := range entries {
		if strings.HasPrefix(name, "bot/") && strings.HasSuffix(name, "config.json") {
			hasConfig = true
		}
	}
	if !hasConfig {
		t.Errorf("archive missing bot/config.json; entries: %v", entries)
	}
}

func TestExportContainsBootstrap(t *testing.T) {
	root := testutil.TempProject(t)
	mgr := bot.NewManager(root)
	cfg := &bot.BotConfig{Name: "bootbot", Goal: "boot test", Model: "test-model"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatal(err)
	}

	b, err := mgr.Get("bootbot")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "bootbot.tar.gz")
	if err := bot.Export(b, outPath); err != nil {
		t.Fatal(err)
	}

	entries := listTarGz(t, outPath)
	found := false
	for _, name := range entries {
		if name == "bootstrap.sh" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("archive missing bootstrap.sh; entries: %v", entries)
	}
}

func TestExportContainsPraxisTomlExample(t *testing.T) {
	root := testutil.TempProject(t)
	mgr := bot.NewManager(root)
	cfg := &bot.BotConfig{Name: "cfgbot", Goal: "cfg test", Model: "test-model"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatal(err)
	}

	b, err := mgr.Get("cfgbot")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "cfgbot.tar.gz")
	if err := bot.Export(b, outPath); err != nil {
		t.Fatal(err)
	}

	entries := listTarGz(t, outPath)
	found := false
	for _, name := range entries {
		if name == "praxis.example.toml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("archive missing praxis.example.toml; entries: %v", entries)
	}
}

func TestExportContainsWorkspaceInToml(t *testing.T) {
	root := testutil.TempProject(t)
	mgr := bot.NewManager(root)
	cfg := &bot.BotConfig{Name: "wsbot", Goal: "ws test", Model: "test-model", Workspace: "myapp"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatal(err)
	}

	b, err := mgr.Get("wsbot")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "wsbot.tar.gz")
	if err := bot.Export(b, outPath); err != nil {
		t.Fatal(err)
	}

	tomlContent := readTarFile(t, outPath, "praxis.example.toml")
	if !strings.Contains(tomlContent, "myapp") {
		t.Errorf("praxis.example.toml missing workspace 'myapp'; content:\n%s", tomlContent)
	}
}

func TestExportInvalidPath(t *testing.T) {
	root := testutil.TempProject(t)
	mgr := bot.NewManager(root)
	cfg := &bot.BotConfig{Name: "failbot", Goal: "fail test", Model: "test-model"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatal(err)
	}

	b, err := mgr.Get("failbot")
	if err != nil {
		t.Fatal(err)
	}

	outPath := filepath.Join(t.TempDir(), "nonexistent", "dir", "out.tar.gz")
	if err := bot.Export(b, outPath); err == nil {
		t.Error("expected error for invalid output path")
	}
}

func listTarGz(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	return names
}

func readTarFile(t *testing.T, archivePath, targetName string) string {
	t.Helper()
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			t.Fatalf("file %q not found in archive", targetName)
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == targetName {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
	}
}
