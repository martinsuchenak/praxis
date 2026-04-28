package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"praxis/internal/bot"
)

// TempProject creates a temporary project directory tree with bots/ and .locks/ subdirs.
// It returns the project root path. Cleanup is registered via t.Cleanup.
func TempProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"bots", ".locks", "lib"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	// Write a minimal botcore.py template
	template := `#!/usr/bin/env scriptling
# --- BOT CONFIG ---
CONFIG = {}
# --- END CONFIG ---
print("hello from bot")
`
	if err := os.WriteFile(filepath.Join(root, "lib", "botcore.py"), []byte(template), 0o644); err != nil {
		t.Fatalf("write botcore.py: %v", err)
	}
	return root
}

// TempBot creates a bot directory in projectRoot/bots/<name> with valid config.json
// and state.json. Returns the bot directory path.
func TempBot(t *testing.T, projectRoot, name string, cfg *bot.BotConfig) string {
	t.Helper()
	if cfg == nil {
		cfg = &bot.BotConfig{}
	}
	cfg.Name = name
	if cfg.Goal == "" {
		cfg.Goal = "test goal"
	}
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}

	botDir := filepath.Join(projectRoot, "bots", name)
	if err := os.MkdirAll(botDir, 0o755); err != nil {
		t.Fatalf("mkdir botDir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(botDir, "entities"), 0o755); err != nil {
		t.Fatalf("mkdir entities: %v", err)
	}

	if err := bot.SaveConfig(botDir, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if err := bot.SaveState(botDir, &bot.BotState{Status: bot.StatusCreated}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	return botDir
}
