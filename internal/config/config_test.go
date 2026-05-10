package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	if cfg.Watchdog.Port != "7700" {
		t.Errorf("default port = %q, want 7700", cfg.Watchdog.Port)
	}
	if cfg.Watchdog.Sandbox != "auto" {
		t.Errorf("default sandbox = %q, want auto", cfg.Watchdog.Sandbox)
	}
	if cfg.Watchdog.MulticastPort != 19373 {
		t.Errorf("default multicast port = %d, want 19373", cfg.Watchdog.MulticastPort)
	}
	if cfg.Bot.TickInterval != 30 {
		t.Errorf("default tick interval = %d, want 30", cfg.Bot.TickInterval)
	}
	if cfg.Bot.TickMaxIter != 5 {
		t.Errorf("default tick max iter = %d, want 5", cfg.Bot.TickMaxIter)
	}
	if cfg.Bot.StaleThreshold != 120 {
		t.Errorf("default stale threshold = %d, want 120", cfg.Bot.StaleThreshold)
	}
	if cfg.Bot.MaxConcurrent != 1 {
		t.Errorf("default max concurrent = %d, want 1", cfg.Bot.MaxConcurrent)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	tomlContent := `
[watchdog]
port = "9999"
sandbox = "none"

[bot]
base_url = "https://example.com/v1"
model = "test-model"
tick_interval = 15

[models]
default = "test-model"

[[models.catalog]]
id = "test-model"
label = "Test"
concurrency = 2

[[workspace]]
name = "myproject"
path = "/tmp/myproject"
secret = "s3cr3t"
scope = "isolated"
`
	if err := os.WriteFile(filepath.Join(dir, "praxis.toml"), []byte(tomlContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Watchdog.Port != "9999" {
		t.Errorf("port = %q, want 9999", cfg.Watchdog.Port)
	}
	if cfg.Watchdog.Sandbox != "none" {
		t.Errorf("sandbox = %q, want none", cfg.Watchdog.Sandbox)
	}
	if cfg.Bot.BaseURL != "https://example.com/v1" {
		t.Errorf("base_url = %q", cfg.Bot.BaseURL)
	}
	if cfg.Bot.Model != "test-model" {
		t.Errorf("model = %q", cfg.Bot.Model)
	}
	if cfg.Bot.TickInterval != 15 {
		t.Errorf("tick_interval = %d, want 15", cfg.Bot.TickInterval)
	}
	if cfg.Models.Default != "test-model" {
		t.Errorf("models.default = %q", cfg.Models.Default)
	}
	if len(cfg.Models.Catalog) != 1 {
		t.Fatalf("catalog len = %d, want 1", len(cfg.Models.Catalog))
	}
	if cfg.Models.Catalog[0].ID != "test-model" {
		t.Errorf("catalog[0].id = %q", cfg.Models.Catalog[0].ID)
	}
	if cfg.Models.Catalog[0].Concurrency != 2 {
		t.Errorf("catalog[0].concurrency = %d, want 2", cfg.Models.Catalog[0].Concurrency)
	}
	if len(cfg.Workspaces) != 1 {
		t.Fatalf("workspaces len = %d, want 1", len(cfg.Workspaces))
	}
	if cfg.Workspaces[0].Name != "myproject" {
		t.Errorf("workspace[0].name = %q", cfg.Workspaces[0].Name)
	}
	if cfg.Workspaces[0].Path != "/tmp/myproject" {
		t.Errorf("workspace[0].path = %q", cfg.Workspaces[0].Path)
	}
	if cfg.Workspaces[0].Secret != "s3cr3t" {
		t.Errorf("workspace[0].secret = %q", cfg.Workspaces[0].Secret)
	}
	if cfg.Workspaces[0].Scope != "isolated" {
		t.Errorf("workspace[0].scope = %q", cfg.Workspaces[0].Scope)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load with no file: %v", err)
	}
	if cfg.Watchdog.Port != "7700" {
		t.Errorf("expected default port, got %q", cfg.Watchdog.Port)
	}
}

func TestEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOT_WATCHDOG_PORT", "8080")
	t.Setenv("BOT_MODEL", "env-model")
	t.Setenv("BOT_TICK_INTERVAL", "20")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Watchdog.Port != "8080" {
		t.Errorf("port = %q, want 8080 (env override)", cfg.Watchdog.Port)
	}
	if cfg.Bot.Model != "env-model" {
		t.Errorf("model = %q, want env-model", cfg.Bot.Model)
	}
	if cfg.Bot.TickInterval != 20 {
		t.Errorf("tick_interval = %d, want 20", cfg.Bot.TickInterval)
	}
}

func TestResolveWorkspace(t *testing.T) {
	cfg := &Config{
		Workspaces: []WorkspaceEntry{
			{Name: "a", Path: "/path/a", Secret: "sa", Scope: "isolated"},
			{Name: "b", Path: "/path/b"},
		},
	}

	p, s, sc, ok := cfg.ResolveWorkspace("a")
	if !ok || p != "/path/a" || s != "sa" || sc != "isolated" {
		t.Errorf("resolve a: ok=%v path=%q secret=%q scope=%q", ok, p, s, sc)
	}

	p, s, sc, ok = cfg.ResolveWorkspace("b")
	if !ok || p != "/path/b" || s != "" || sc != "" {
		t.Errorf("resolve b: ok=%v path=%q secret=%q scope=%q", ok, p, s, sc)
	}

	_, _, _, ok = cfg.ResolveWorkspace("missing")
	if ok {
		t.Error("expected not found for missing workspace")
	}
}

func TestSetRemoveWorkspace(t *testing.T) {
	cfg := &Config{}

	cfg.SetWorkspace(WorkspaceEntry{Name: "a", Path: "/a"})
	if len(cfg.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(cfg.Workspaces))
	}

	cfg.SetWorkspace(WorkspaceEntry{Name: "a", Path: "/a-updated"})
	if cfg.Workspaces[0].Path != "/a-updated" {
		t.Errorf("path = %q, want /a-updated", cfg.Workspaces[0].Path)
	}

	cfg.SetWorkspace(WorkspaceEntry{Name: "b", Path: "/b"})
	if len(cfg.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(cfg.Workspaces))
	}

	if !cfg.RemoveWorkspace("a") {
		t.Error("RemoveWorkspace should return true")
	}
	if len(cfg.Workspaces) != 1 || cfg.Workspaces[0].Name != "b" {
		t.Errorf("after remove: %v", cfg.Workspaces)
	}

	if cfg.RemoveWorkspace("nonexistent") {
		t.Error("RemoveWorkspace should return false for missing")
	}
}

func TestSave(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Workspaces: []WorkspaceEntry{
			{Name: "test", Path: "/tmp/test"},
		},
	}
	setDefaults(cfg)

	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := filepath.Join(dir, "praxis.toml")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not created: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Workspaces) != 1 {
		t.Errorf("loaded workspaces len = %d, want 1", len(loaded.Workspaces))
	}
	if loaded.Workspaces[0].Name != "test" {
		t.Errorf("loaded workspace name = %q", loaded.Workspaces[0].Name)
	}
}

func TestModelsAsInterface(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Catalog: []ModelEntry{
				{ID: "m1", Label: "M1", Concurrency: 2, Strengths: []string{"fast"}, ThinkingTemplate: "qwen"},
				{ID: "m2", Label: "M2", Concurrency: 1},
			},
		},
	}

	out := cfg.ModelsAsInterface()
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}

	m1, ok := out[0].(map[string]interface{})
	if !ok {
		t.Fatal("expected map")
	}
	if m1["id"] != "m1" {
		t.Errorf("id = %v", m1["id"])
	}
	if m1["concurrency"] != 2 {
		t.Errorf("concurrency = %v", m1["concurrency"])
	}
	if m1["thinking_template"] != "qwen" {
		t.Errorf("thinking_template = %v", m1["thinking_template"])
	}

	m2, _ := out[1].(map[string]interface{})
	if _, has := m2["thinking_template"]; has {
		t.Error("m2 should not have thinking_template")
	}
}

func TestClusterAddrs(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)

	if cfg.ClusterBindAddr() != "0.0.0.0:7700" {
		t.Errorf("bind = %q", cfg.ClusterBindAddr())
	}
	if cfg.ClusterAdvertiseAddr() != "0.0.0.0:7700" {
		t.Errorf("advertise = %q", cfg.ClusterAdvertiseAddr())
	}

	cfg.Watchdog.Advertise = "1.2.3.4:7700"
	if cfg.ClusterAdvertiseAddr() != "1.2.3.4:7700" {
		t.Errorf("advertise with override = %q", cfg.ClusterAdvertiseAddr())
	}
}

func TestGetSet(t *testing.T) {
	cfg := &Config{}
	Set(cfg)
	if got := Get(); got != cfg {
		t.Error("Get/Set roundtrip failed")
	}
}

func TestInitDirProject(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "myproject")

	created, err := InitDir(projectDir)
	if err != nil {
		t.Fatalf("InitDir: %v", err)
	}
	if created != projectDir {
		t.Errorf("created = %q, want %q", created, projectDir)
	}

	cfgPath := filepath.Join(projectDir, "config.toml")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.toml not created: %v", err)
	}

	cfg, err := Load(projectDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Watchdog.Port != "7700" {
		t.Errorf("port = %q, want 7700", cfg.Watchdog.Port)
	}
}

func TestInitDirAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "existing")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "config.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := InitDir(projectDir)
	if err == nil {
		t.Error("expected error when config already exists")
	}
}

func TestParseCSV(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ", []string{"a", "b"}},
		{",,a,,", []string{"a"}},
	}
	for _, tc := range cases {
		got := parseCSV(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseCSV(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseCSV(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestTsnetDirOrDefault(t *testing.T) {
	cfg := &Config{}
	if got := cfg.TsnetDirOrDefault("/project"); got != "/project/.tsnet" {
		t.Errorf("default = %q, want /project/.tsnet", got)
	}

	cfg.Tsnet.Dir = "/custom/tsnet"
	if got := cfg.TsnetDirOrDefault("/project"); got != "/custom/tsnet" {
		t.Errorf("override = %q, want /custom/tsnet", got)
	}
}

func TestModelsDirResolved(t *testing.T) {
	dir := t.TempDir()
	modelsDir := filepath.Join(dir, "models")
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{}
	got := cfg.ModelsDirResolved(dir)
	if got != modelsDir {
		t.Errorf("default = %q, want %q", got, modelsDir)
	}

	cfg.Watchdog.ModelsDir = modelsDir
	got = cfg.ModelsDirResolved(dir)
	if got != modelsDir {
		t.Errorf("explicit = %q, want %q", got, modelsDir)
	}

	cfg.Watchdog.ModelsDir = "/nonexistent/path"
	got = cfg.ModelsDirResolved(dir)
	if got != "" {
		t.Errorf("missing dir = %q, want empty", got)
	}
}

func TestApplySecretsToEnv(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)
	cfg.Bot.APIKey = "test-api-key"
	cfg.Bot.BaseURL = "https://api.test.com/v1"
	cfg.Bot.GossipSecret = "test-secret"

	applySecretsToEnv(cfg)

	if os.Getenv("BOT_API_KEY") != "test-api-key" {
		t.Errorf("BOT_API_KEY = %q", os.Getenv("BOT_API_KEY"))
	}
	if os.Getenv("BOT_BASE_URL") != "https://api.test.com/v1" {
		t.Errorf("BOT_BASE_URL = %q", os.Getenv("BOT_BASE_URL"))
	}
	if os.Getenv("BOT_GOSSIP_SECRET") != "test-secret" {
		t.Errorf("BOT_GOSSIP_SECRET = %q", os.Getenv("BOT_GOSSIP_SECRET"))
	}
}

func TestApplySecretsToEnvEmpty(t *testing.T) {
	_ = os.Unsetenv("BOT_API_KEY")
	_ = os.Unsetenv("BOT_BASE_URL")
	_ = os.Unsetenv("BOT_GOSSIP_SECRET")

	cfg := &Config{}
	applySecretsToEnv(cfg)

	if os.Getenv("BOT_API_KEY") != "" {
		t.Errorf("BOT_API_KEY should be empty when config is empty")
	}
}

func TestGlobalDir(t *testing.T) {
	t.Setenv("PRAXIS_HOME", "/custom/home")
	if got := GlobalDir(); got != "/custom/home" {
		t.Errorf("PRAXIS_HOME = %q, want /custom/home", got)
	}

	_ = os.Unsetenv("PRAXIS_HOME")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := GlobalDir(); got != "/xdg/praxis" {
		t.Errorf("XDG = %q, want /xdg/praxis", got)
	}

	_ = os.Unsetenv("XDG_CONFIG_HOME")
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".config", "praxis")
	if got := GlobalDir(); got != expected {
		t.Errorf("default = %q, want %q", got, expected)
	}
}

func TestLoadBadTOML(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "praxis.toml")
	if err := os.WriteFile(bad, []byte("this is not valid toml {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil {
		t.Error("expected error for bad TOML")
	}
}

func TestSaveBadPath(t *testing.T) {
	cfg := &Config{}
	setDefaults(cfg)
	err := cfg.Save("/nonexistent/deep/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for bad save path")
	}
}

func TestEnvOverrideSeeds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOT_SEED_ADDRS", "10.0.0.1:7700,10.0.0.2:7700")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Watchdog.Seeds) != 2 {
		t.Fatalf("seeds = %v, want 2 entries", cfg.Watchdog.Seeds)
	}
	if cfg.Watchdog.Seeds[0] != "10.0.0.1:7700" {
		t.Errorf("seed[0] = %q", cfg.Watchdog.Seeds[0])
	}
}

func TestEnvOverrideTsnet(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOT_TSNET_HOSTNAME", "myhost")
	t.Setenv("TS_AUTHKEY", "tskey-secret")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tsnet.Hostname != "myhost" {
		t.Errorf("hostname = %q", cfg.Tsnet.Hostname)
	}
	if cfg.Tsnet.AuthKey != "tskey-secret" {
		t.Errorf("authkey = %q", cfg.Tsnet.AuthKey)
	}
}

func TestEnvOverrideBotBools(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOT_LOG_VERBOSE", "true")
	t.Setenv("BOT_AUTH_DISABLED", "true")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Bot.LogVerbose {
		t.Error("log_verbose should be true")
	}
	if !cfg.Watchdog.AuthDisabled {
		t.Error("auth_disabled should be true")
	}
}

func TestEnvOverrideAllowlist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOT_SHELL_ALLOWLIST", "ls,cat,grep")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Watchdog.Allowlist) != 3 || cfg.Watchdog.Allowlist[0] != "ls" {
		t.Errorf("allowlist = %v", cfg.Watchdog.Allowlist)
	}
}

func TestModelsAsInterfaceEmpty(t *testing.T) {
	cfg := &Config{}
	out := cfg.ModelsAsInterface()
	if out != nil {
		t.Errorf("expected nil for empty catalog, got %v", out)
	}
}

func TestModelsAsInterfaceWithPerModelURL(t *testing.T) {
	cfg := &Config{
		Models: ModelsConfig{
			Catalog: []ModelEntry{
				{ID: "m1", BaseURL: "http://localhost:11434/v1", APIKey: "ollama"},
			},
		},
	}
	out := cfg.ModelsAsInterface()
	m1 := out[0].(map[string]interface{})
	if m1["base_url"] != "http://localhost:11434/v1" {
		t.Errorf("base_url = %v", m1["base_url"])
	}
	if m1["api_key"] != "ollama" {
		t.Errorf("api_key = %v", m1["api_key"])
	}
}

func TestSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Workspaces: []WorkspaceEntry{
			{Name: "a", Path: "/a", Secret: "sa", Scope: "isolated"},
			{Name: "b", Path: "/b"},
		},
		Models: ModelsConfig{
			Default: "glm-5",
			Catalog: []ModelEntry{
				{ID: "glm-5", Label: "GLM 5", Concurrency: 2, ThinkingTemplate: "glm"},
			},
		},
	}
	setDefaults(cfg)
	cfg.Watchdog.Port = "9999"

	if err := cfg.Save(dir); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Watchdog.Port != "9999" {
		t.Errorf("port = %q, want 9999", loaded.Watchdog.Port)
	}
	if loaded.Models.Default != "glm-5" {
		t.Errorf("models.default = %q", loaded.Models.Default)
	}
	if len(loaded.Models.Catalog) != 1 {
		t.Errorf("catalog len = %d", len(loaded.Models.Catalog))
	}
	if loaded.Models.Catalog[0].ThinkingTemplate != "glm" {
		t.Errorf("thinking_template = %q", loaded.Models.Catalog[0].ThinkingTemplate)
	}
	if len(loaded.Workspaces) != 2 {
		t.Errorf("workspaces len = %d", len(loaded.Workspaces))
	}
}
