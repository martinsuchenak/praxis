package cmd

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"praxis/internal/bot"
	"praxis/internal/testutil"
)

func TestImportBotBasic(t *testing.T) {
	root := testutil.TempProject(t)
	botsDir := filepath.Join(root, "bots")
	archivePath := createTestArchive(t, "impbot", "import test", "", nil)

	name, err := importBot(archivePath, botsDir, "", nil)
	if err != nil {
		t.Fatalf("importBot: %v", err)
	}
	if name != "impbot" {
		t.Errorf("name = %q, want %q", name, "impbot")
	}

	cfg, err := bot.LoadConfig(filepath.Join(botsDir, "impbot"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Name != "impbot" {
		t.Errorf("config name = %q", cfg.Name)
	}
	if cfg.Goal != "import test" {
		t.Errorf("config goal = %q", cfg.Goal)
	}
}

func TestImportBotNameOverride(t *testing.T) {
	root := testutil.TempProject(t)
	botsDir := filepath.Join(root, "bots")
	archivePath := createTestArchive(t, "origbot", "goal", "", nil)

	name, err := importBot(archivePath, botsDir, "newbot", nil)
	if err != nil {
		t.Fatalf("importBot: %v", err)
	}
	if name != "newbot" {
		t.Errorf("name = %q, want %q", name, "newbot")
	}

	cfg, err := bot.LoadConfig(filepath.Join(botsDir, "newbot"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Name != "newbot" {
		t.Errorf("config name = %q, want %q", cfg.Name, "newbot")
	}
}

func TestImportBotAlreadyExists(t *testing.T) {
	root := testutil.TempProject(t)
	botsDir := filepath.Join(root, "bots")
	testutil.TempBot(t, root, "dupbot", &bot.BotConfig{Goal: "orig", Model: "test"})

	archivePath := createTestArchive(t, "dupbot", "new goal", "", nil)

	_, err := importBot(archivePath, botsDir, "", nil)
	if err == nil {
		t.Fatal("expected error for duplicate bot")
	}
}

func TestImportBotWorkspaceRemap(t *testing.T) {
	root := testutil.TempProject(t)
	botsDir := filepath.Join(root, "bots")
	archivePath := createTestArchive(t, "wsbot", "goal", "myapp", nil)

	wsMappings := map[string]string{"myapp": "/new/path/myapp"}
	name, err := importBot(archivePath, botsDir, "", wsMappings)
	if err != nil {
		t.Fatalf("importBot: %v", err)
	}

	cfg, err := bot.LoadConfig(filepath.Join(botsDir, name))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.WorkspacePath != "/new/path/myapp" {
		t.Errorf("WorkspacePath = %q, want %q", cfg.WorkspacePath, "/new/path/myapp")
	}
}

func TestImportBotMissingConfig(t *testing.T) {
	root := testutil.TempProject(t)
	botsDir := filepath.Join(root, "bots")
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "noconfig.tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "bot/brain.md", Size: 4, Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = importBot(archivePath, botsDir, "", nil)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestImportBotNonexistentArchive(t *testing.T) {
	_, err := importBot("/nonexistent/path.tar.gz", "/tmp/bots", "", nil)
	if err == nil {
		t.Fatal("expected error for missing archive")
	}
}

func TestImportBotPreservesFiles(t *testing.T) {
	root := testutil.TempProject(t)
	botsDir := filepath.Join(root, "bots")
	extraFiles := map[string]string{
		"brain.md":          "# brain content",
		"notes.txt":         "some notes",
		"entities/test.ent": "entity data",
	}
	archivePath := createTestArchive(t, "filebot", "goal", "", extraFiles)

	name, err := importBot(archivePath, botsDir, "", nil)
	if err != nil {
		t.Fatalf("importBot: %v", err)
	}

	botDir := filepath.Join(botsDir, name)
	for fname, content := range extraFiles {
		data, err := os.ReadFile(filepath.Join(botDir, fname))
		if err != nil {
			t.Errorf("file %q missing: %v", fname, err)
			continue
		}
		if string(data) != content {
			t.Errorf("file %q: got %q, want %q", fname, string(data), content)
		}
	}
}

func createTestArchive(t *testing.T, botName, goal, workspace string, extraFiles map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, botName+".tar.gz")

	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	defer func() {
		_ = tw.Close()
		_ = gw.Close()
		_ = f.Close()
	}()

	cfg := &bot.BotConfig{
		Name:      botName,
		Goal:      goal,
		Model:     "test-model",
		Workspace: workspace,
	}
	cfgData, _ := json.MarshalIndent(cfg, "", "  ")
	writeTarFile(t, tw, "bot/config.json", cfgData)

	if err := tw.WriteHeader(&tar.Header{Name: "bot/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "bot/entities/", Typeflag: tar.TypeDir, Mode: 0755}); err != nil {
		t.Fatal(err)
	}

	for fname, content := range extraFiles {
		writeTarFile(t, tw, "bot/"+fname, []byte(content))
	}
	return archivePath
}

func writeTarFile(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
}
