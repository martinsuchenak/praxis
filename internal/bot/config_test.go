package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name    string
		wantErr bool
	}{
		{"Devbot", false},
		{"my-bot_1", false},
		{"A", false},
		{"", true},
		{"has space", true},
		{"has/slash", true},
		{"has@symbol", true},
		{strings.Repeat("a", 65), true},
		{strings.Repeat("a", 64), false},
	}
	for _, tc := range cases {
		err := ValidateName(tc.name)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateName(%q) error=%v, wantErr=%v", tc.name, err, tc.wantErr)
		}
	}
}

func TestValidateChildScope(t *testing.T) {
	cases := []struct {
		parent  string
		child   string
		wantErr bool
	}{
		// open parent allows everything
		{ScopeOpen, ScopeOpen, false},
		{ScopeOpen, ScopeIsolated, false},
		{ScopeOpen, ScopeFamily, false},
		{ScopeOpen, ScopeGateway, false},
		// isolated parent: no open, no gateway
		{ScopeIsolated, ScopeIsolated, false},
		{ScopeIsolated, ScopeFamily, false},
		{ScopeIsolated, ScopeOpen, true},
		{ScopeIsolated, ScopeGateway, true},
		// family parent: only family
		{ScopeFamily, ScopeFamily, false},
		{ScopeFamily, ScopeOpen, true},
		{ScopeFamily, ScopeIsolated, true},
		{ScopeFamily, ScopeGateway, true},
		// gateway parent: gateway, isolated, family allowed
		{ScopeGateway, ScopeGateway, false},
		{ScopeGateway, ScopeIsolated, false},
		{ScopeGateway, ScopeFamily, false},
		{ScopeGateway, ScopeOpen, true},
		// empty child scope is always OK
		{ScopeIsolated, "", false},
		// empty parent scope (no scope set) allows everything
		{"", ScopeOpen, false},
		{"", ScopeGateway, false},
	}
	for _, tc := range cases {
		err := ValidateChildScope(tc.parent, tc.child)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateChildScope(%q, %q) error=%v, wantErr=%v", tc.parent, tc.child, err, tc.wantErr)
		}
	}
}

func TestAllowedPaths(t *testing.T) {
	cfg := &BotConfig{Name: "testbot"}
	botDir := "/bots/testbot"
	botsDir := "/bots"
	locksDir := "/locks"

	paths := cfg.AllowedPaths(botDir, botsDir, locksDir)
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths without workspace, got %d", len(paths))
	}

	cfg.WorkspacePath = "/workspace/myproject"
	paths = cfg.AllowedPaths(botDir, botsDir, locksDir)
	if len(paths) != 4 {
		t.Fatalf("expected 4 paths with workspace, got %d", len(paths))
	}
	found := false
	for _, p := range paths {
		if p == "/workspace/myproject" {
			found = true
		}
	}
	if !found {
		t.Error("workspace_path not in allowed paths")
	}
}

func TestSaveAndLoadConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := &BotConfig{
		Name:          "mybot",
		Goal:          "do stuff",
		Model:         "gpt-4",
		Thinking:      true,
		WorkspacePath: "/tmp/ws",
		Scope:         ScopeIsolated,
	}

	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if loaded.Name != cfg.Name {
		t.Errorf("Name: got %q want %q", loaded.Name, cfg.Name)
	}
	if loaded.Goal != cfg.Goal {
		t.Errorf("Goal: got %q want %q", loaded.Goal, cfg.Goal)
	}
	if loaded.Scope != ScopeIsolated {
		t.Errorf("Scope: got %q want %q", loaded.Scope, ScopeIsolated)
	}
	if loaded.CreatedAt == 0 {
		t.Error("CreatedAt should be set by SaveConfig")
	}
}

func TestSaveConfigAtomic(t *testing.T) {
	dir := t.TempDir()
	cfg := &BotConfig{Name: "atomicbot", Goal: "test"}

	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// tmp file must not remain
	if _, err := os.Stat(filepath.Join(dir, configFile+".tmp")); !os.IsNotExist(err) {
		t.Error("tmp file should not exist after atomic write")
	}
}

func TestSaveConfigSetsCreatedAt(t *testing.T) {
	dir := t.TempDir()
	before := time.Now().Unix()
	cfg := &BotConfig{Name: "bot"}
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Unix()

	loaded, _ := LoadConfig(dir)
	if loaded.CreatedAt < before || loaded.CreatedAt > after {
		t.Errorf("CreatedAt %d outside [%d, %d]", loaded.CreatedAt, before, after)
	}
}

func TestSaveConfigPreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	cfg := &BotConfig{Name: "bot", CreatedAt: 1000}
	if err := SaveConfig(dir, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, _ := LoadConfig(dir)
	if loaded.CreatedAt != 1000 {
		t.Errorf("CreatedAt overwritten: got %d want 1000", loaded.CreatedAt)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := LoadConfig(t.TempDir())
	if err == nil {
		t.Error("expected error for missing config.json")
	}
}

func TestLoadConfigBadJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, configFile), []byte("{bad json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(dir)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestAsDict(t *testing.T) {
	cfg := &BotConfig{
		Name:     "b",
		Goal:     "g",
		Model:    "m",
		Thinking: true,
		Scope:    ScopeOpen,
	}
	d := cfg.AsDict()
	if d["name"] != "b" {
		t.Errorf("name: %v", d["name"])
	}
	if d["thinking"] != true {
		t.Errorf("thinking: %v", d["thinking"])
	}
	// seed_addrs must always be present and a slice (not nil)
	seeds, ok := d["seed_addrs"].([]string)
	if !ok || seeds == nil {
		t.Errorf("seed_addrs should be []string, got %T %v", d["seed_addrs"], d["seed_addrs"])
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &BotConfig{
		Name:              "roundtrip",
		Goal:              "survive",
		Model:             "claude-3",
		Thinking:          false,
		Workspace:         "ws1",
		WorkspacePath:     "/data/ws1",
		Scope:             ScopeGateway,
		AllowedWorkspaces: []string{"ws2", "ws3"},
		Parent:            "parentbot",
		GossipSecret:      "s3cr3t",
		CreatedAt:         9999,
	}
	if err := SaveConfig(dir, original); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	data1, _ := json.Marshal(original)
	data2, _ := json.Marshal(loaded)
	if string(data1) != string(data2) {
		t.Errorf("round-trip mismatch:\n  got  %s\n  want %s", data2, data1)
	}
}
