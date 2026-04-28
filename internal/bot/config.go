package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const configFile = "config.json"

// Scope constants control bot peer visibility.
const (
	ScopeOpen     = "open"
	ScopeIsolated = "isolated"
	ScopeFamily   = "family"
	ScopeGateway  = "gateway"
)

// allowedChildScopes maps a parent scope to the set of scopes a child may have.
var allowedChildScopes = map[string]map[string]bool{
	ScopeOpen:     {ScopeOpen: true, ScopeIsolated: true, ScopeFamily: true, ScopeGateway: true},
	ScopeGateway:  {ScopeGateway: true, ScopeIsolated: true, ScopeFamily: true},
	ScopeIsolated: {ScopeIsolated: true, ScopeFamily: true},
	ScopeFamily:   {ScopeFamily: true},
	"":            {ScopeOpen: true, ScopeIsolated: true, ScopeFamily: true, ScopeGateway: true},
}

// BotConfig is the controller-owned configuration for a bot.
// It is written only by praxis and is not in the bot's allowed filesystem paths.
type BotConfig struct {
	Name              string   `json:"name"`
	Goal              string   `json:"goal"`
	Model             string   `json:"model"`
	Thinking          bool     `json:"thinking"`
	Brain             string   `json:"brain,omitempty"`
	Workspace         string   `json:"workspace,omitempty"`
	WorkspacePath     string   `json:"workspace_path,omitempty"`
	Scope             string   `json:"scope,omitempty"`
	AllowedWorkspaces []string `json:"allowed_workspaces,omitempty"`
	Parent            string   `json:"parent,omitempty"`
	GossipSecret      string   `json:"gossip_secret,omitempty"`
	CreatedAt         int64    `json:"created_at"`
}

// AllowedPaths returns the filesystem paths the bot interpreter may access.
// Derived solely from config — never from state or bot.py.
func (c *BotConfig) AllowedPaths(botDir, botsDir, locksDir string) []string {
	paths := []string{botDir, botsDir, locksDir}
	if c.WorkspacePath != "" {
		paths = append(paths, c.WorkspacePath)
	}
	return paths
}

// AsDict returns the config as a map suitable for scriptling SetVar injection.
func (c *BotConfig) AsDict() map[string]interface{} {
	return map[string]interface{}{
		"name":               c.Name,
		"goal":               c.Goal,
		"model":              c.Model,
		"thinking":           c.Thinking,
		"brain":              c.Brain,
		"workspace":          c.Workspace,
		"workspace_path":     c.WorkspacePath,
		"scope":              c.Scope,
		"allowed_workspaces": c.AllowedWorkspaces,
		"parent":             c.Parent,
		"gossip_secret":      c.GossipSecret,
		"seed_addrs":         []string{},
	}
}

// ValidateName returns an error if name is not a valid bot identifier.
func ValidateName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("name must be 1–64 characters")
	}
	for _, c := range name {
		if !isNameChar(c) {
			return fmt.Errorf("name %q: only letters, digits, dash, underscore allowed", name)
		}
	}
	return nil
}

func isNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '_'
}

// ValidateChildScope returns an error if the child scope is not permitted given the parent scope.
func ValidateChildScope(parentScope, childScope string) error {
	allowed, ok := allowedChildScopes[parentScope]
	if !ok {
		return fmt.Errorf("unknown parent scope %q", parentScope)
	}
	if childScope == "" {
		return nil
	}
	if !allowed[childScope] {
		return fmt.Errorf("scope %q is not permitted for a child of scope %q", childScope, parentScope)
	}
	return nil
}

// LoadConfig reads config.json from botDir.
func LoadConfig(botDir string) (*BotConfig, error) {
	data, err := os.ReadFile(filepath.Join(botDir, configFile))
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg BotConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
}

// SaveConfig atomically writes config.json to botDir.
func SaveConfig(botDir string, cfg *BotConfig) error {
	if cfg.CreatedAt == 0 {
		cfg.CreatedAt = time.Now().Unix()
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return atomicWrite(filepath.Join(botDir, configFile), data)
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
