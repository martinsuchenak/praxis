package bot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Bot combines a bot's config and state for callers that need both.
type Bot struct {
	Config *BotConfig
	State  *BotState
	Dir    string
}

// IsStale returns true if the bot appears running but has not ticked within threshold.
func (b *Bot) IsStale(threshold time.Duration) bool {
	if b.State.Status != StatusRunning {
		return false
	}
	if b.State.LastTickTS == 0 {
		return false
	}
	return time.Since(time.Unix(b.State.LastTickTS, 0)) > threshold
}

// Manager handles bot filesystem operations.
type Manager struct {
	BotsDir       string
	LocksDir      string
	TemplatePath  string
	TemplateBytes []byte // takes precedence over TemplatePath when non-nil
}

// NewManager creates a Manager rooted at projectDir.
func NewManager(projectDir string) *Manager {
	return &Manager{
		BotsDir:      filepath.Join(projectDir, "bots"),
		LocksDir:     filepath.Join(projectDir, ".locks"),
		TemplatePath: filepath.Join(projectDir, "lib", "botcore.py"),
	}
}

// Create writes a new bot directory with config.json, state.json, entities/,
// and a copy of botcore.py. Returns an error if the bot already exists.
func (m *Manager) Create(cfg *BotConfig) error {
	if err := ValidateName(cfg.Name); err != nil {
		return err
	}
	botDir := m.BotDir(cfg.Name)
	if _, err := os.Stat(botDir); err == nil {
		return fmt.Errorf("bot already exists: %s", cfg.Name)
	}

	if err := os.MkdirAll(botDir, 0o755); err != nil {
		return fmt.Errorf("create bot dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(botDir, "entities"), 0o755); err != nil {
		return fmt.Errorf("create entities dir: %w", err)
	}

	if err := SaveConfig(botDir, cfg); err != nil {
		_ = os.RemoveAll(botDir)
		return fmt.Errorf("save config: %w", err)
	}
	if err := SaveState(botDir, &BotState{Status: StatusCreated}); err != nil {
		_ = os.RemoveAll(botDir)
		return fmt.Errorf("save state: %w", err)
	}
	if err := m.copyTemplate(botDir); err != nil {
		_ = os.RemoveAll(botDir)
		return fmt.Errorf("copy template: %w", err)
	}

	// Copy scriptling reference doc if present
	refSrc := filepath.Join(filepath.Dir(m.TemplatePath), "scriptling-reference.md")
	if _, err := os.Stat(refSrc); err == nil {
		_ = copyFile(refSrc, filepath.Join(botDir, "entities", "scriptling-reference.md"))
	}

	return nil
}

// Get loads a single bot by ID. Returns an error if not found.
func (m *Manager) Get(id string) (*Bot, error) {
	if err := ValidateName(id); err != nil {
		return nil, err
	}
	botDir := m.BotDir(id)
	cfg, err := LoadConfig(botDir)
	if err != nil {
		return nil, fmt.Errorf("bot %q: %w", id, err)
	}
	state, err := LoadState(botDir)
	if err != nil {
		return nil, fmt.Errorf("bot %q state: %w", id, err)
	}
	return &Bot{Config: cfg, State: state, Dir: botDir}, nil
}

// List returns all bots found in BotsDir. Entries with missing or unparseable
// config.json are silently skipped.
func (m *Manager) List() ([]*Bot, error) {
	entries, err := os.ReadDir(m.BotsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bots dir: %w", err)
	}

	var bots []*Bot
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := m.Get(e.Name())
		if err != nil {
			continue // skip invalid entries
		}
		bots = append(bots, b)
	}
	return bots, nil
}

// SetStatus updates only the Status field of state.json.
func (m *Manager) SetStatus(id, status string) error {
	botDir := m.BotDir(id)
	state, err := LoadState(botDir)
	if err != nil {
		return err
	}
	state.Status = status
	return SaveState(botDir, state)
}

// Delete kills any running process and removes the bot directory.
func (m *Manager) Delete(id string) error {
	if err := ValidateName(id); err != nil {
		return err
	}
	m.RemoveLocks(id)
	botDir := m.BotDir(id)
	if _, err := os.Stat(botDir); os.IsNotExist(err) {
		return fmt.Errorf("bot not found: %s", id)
	}
	return os.RemoveAll(botDir)
}

// RemoveLocks deletes any LLM queue ticket files for the given bot.
func (m *Manager) RemoveLocks(botID string) {
	suffix := "_" + botID + ".wait"
	entries, err := os.ReadDir(m.LocksDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(m.LocksDir, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name(), suffix) {
				_ = os.Remove(filepath.Join(m.LocksDir, e.Name(), f.Name()))
			}
		}
	}
}

// BotDir returns the directory path for a bot ID.
func (m *Manager) BotDir(id string) string {
	return filepath.Join(m.BotsDir, id)
}

// BotScript returns the path to bot.py for a given bot ID.
func (m *Manager) BotScript(id string) string {
	return filepath.Join(m.BotDir(id), "bot.py")
}

func (m *Manager) copyTemplate(botDir string) error {
	dst := filepath.Join(botDir, "bot.py")
	if len(m.TemplateBytes) > 0 {
		return os.WriteFile(dst, m.TemplateBytes, 0o644)
	}
	return copyFile(m.TemplatePath, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
