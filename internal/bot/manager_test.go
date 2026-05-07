package bot_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"praxis/internal/bot"
	"praxis/internal/testutil"
)

func TestUpdateConfig(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "upbot", &bot.BotConfig{
		Model:    "old-model",
		Thinking: false,
		Goal:     "old goal",
		Scope:    bot.ScopeIsolated,
	})

	err := m.UpdateConfig("upbot", map[string]string{
		"model":    "new-model",
		"thinking": "true",
		"goal":     "new goal",
		"scope":    bot.ScopeGateway,
	})
	if err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	loaded, err := bot.LoadConfig(m.BotDir("upbot"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Model != "new-model" {
		t.Errorf("Model: got %q, want %q", loaded.Model, "new-model")
	}
	if !loaded.Thinking {
		t.Errorf("Thinking: got false, want true")
	}
	if loaded.Goal != "new goal" {
		t.Errorf("Goal: got %q, want %q", loaded.Goal, "new goal")
	}
	if loaded.Scope != bot.ScopeGateway {
		t.Errorf("Scope: got %q, want %q", loaded.Scope, bot.ScopeGateway)
	}
}

func TestUpdateConfigBotNotFound(t *testing.T) {
	m, _ := newTestManager(t)
	err := m.UpdateConfig("ghost", map[string]string{"model": "x"})
	if err == nil {
		t.Error("expected error for nonexistent bot")
	}
}

func TestRefreshTemplate(t *testing.T) {
	m, _ := newTestManager(t)
	m.TemplateBytes = []byte("original")

	cfg := &bot.BotConfig{Name: "refbot", Goal: "g", Model: "m"}
	if err := m.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	m.TemplateBytes = []byte("updated template")
	if err := m.RefreshTemplate("refbot"); err != nil {
		t.Fatalf("RefreshTemplate: %v", err)
	}

	got, err := os.ReadFile(m.BotScript("refbot"))
	if err != nil {
		t.Fatalf("read bot.py: %v", err)
	}
	if string(got) != "updated template" {
		t.Errorf("bot.py: got %q, want %q", got, "updated template")
	}
}

func TestRefreshTemplateInvalidName(t *testing.T) {
	m, _ := newTestManager(t)
	err := m.RefreshTemplate("bad name!")
	if err == nil {
		t.Error("expected error for invalid name")
	}
}

func TestRemoveLocks(t *testing.T) {
	m, _ := newTestManager(t)

	lockDir := filepath.Join(m.LocksDir, "mymodel")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lockFile := filepath.Join(lockDir, "_mybot.wait")
	if err := os.WriteFile(lockFile, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	otherFile := filepath.Join(lockDir, "_otherbot.wait")
	if err := os.WriteFile(otherFile, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	m.RemoveLocks("mybot")

	if _, err := os.Stat(lockFile); !os.IsNotExist(err) {
		t.Error("lock file for mybot should be removed")
	}
	if _, err := os.Stat(otherFile); err != nil {
		t.Error("lock file for otherbot should still exist")
	}
}

func TestRemoveLocksNoDir(t *testing.T) {
	m, _ := newTestManager(t)
	m.LocksDir = filepath.Join(t.TempDir(), "nonexistent")
	m.RemoveLocks("anybot")
}

func newTestManager(t *testing.T) (*bot.Manager, string) {
	t.Helper()
	root := testutil.TempProject(t)
	return bot.NewManager(root), root
}

func TestCreateBot(t *testing.T) {
	m, _ := newTestManager(t)
	cfg := &bot.BotConfig{Name: "devbot", Goal: "build stuff", Model: "gpt-4"}

	if err := m.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// bot directory exists
	if _, err := os.Stat(m.BotDir("devbot")); err != nil {
		t.Errorf("bot dir missing: %v", err)
	}
	// config.json written
	loaded, err := bot.LoadConfig(m.BotDir("devbot"))
	if err != nil {
		t.Fatalf("bot.LoadConfig: %v", err)
	}
	if loaded.Name != "devbot" {
		t.Errorf("Name: %q", loaded.Name)
	}
	// state.json written with bot.StatusCreated
	state, err := bot.LoadState(m.BotDir("devbot"))
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != bot.StatusCreated {
		t.Errorf("initial status: %q", state.Status)
	}
	// bot.py copied
	if _, err := os.Stat(m.BotScript("devbot")); err != nil {
		t.Errorf("bot.py missing: %v", err)
	}
	// entities dir exists
	if _, err := os.Stat(filepath.Join(m.BotDir("devbot"), "entities")); err != nil {
		t.Errorf("entities dir missing: %v", err)
	}
}

func TestCreateBotDuplicateReturnsError(t *testing.T) {
	m, _ := newTestManager(t)
	cfg := &bot.BotConfig{Name: "dup", Goal: "x", Model: "y"}
	if err := m.Create(cfg); err != nil {
		t.Fatal(err)
	}
	if err := m.Create(cfg); err == nil {
		t.Error("expected error for duplicate bot")
	}
}

func TestCreateBotInvalidName(t *testing.T) {
	m, _ := newTestManager(t)
	cfg := &bot.BotConfig{Name: "bad name!", Goal: "x", Model: "y"}
	if err := m.Create(cfg); err == nil {
		t.Error("expected error for invalid name")
	}
}

func TestGetBot(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "getme", &bot.BotConfig{Goal: "get this bot"})

	b, err := m.Get("getme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if b.Config.Goal != "get this bot" {
		t.Errorf("Goal: %q", b.Config.Goal)
	}
	if b.Dir != m.BotDir("getme") {
		t.Errorf("Dir: %q", b.Dir)
	}
}

func TestGetBotNotFound(t *testing.T) {
	m, _ := newTestManager(t)
	_, err := m.Get("ghost")
	if err == nil {
		t.Error("expected error for missing bot")
	}
}

func TestListBots(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "alpha", nil)
	testutil.TempBot(t, root, "beta", nil)
	testutil.TempBot(t, root, "gamma", nil)

	bots, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bots) != 3 {
		t.Errorf("expected 3 bots, got %d", len(bots))
	}
}

func TestListBotsEmptyDir(t *testing.T) {
	m, _ := newTestManager(t)
	bots, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(bots) != 0 {
		t.Errorf("expected 0 bots, got %d", len(bots))
	}
}

func TestListBotsSkipsMissingConfig(t *testing.T) {
	m, _ := newTestManager(t)
	// create a dir with no config.json (invalid entry, should be skipped)
	_ = os.MkdirAll(filepath.Join(m.BotsDir, "invalid"), 0o755)

	bots, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(bots) != 0 {
		t.Errorf("expected 0 bots, got %d: invalid entry should be skipped", len(bots))
	}
}

func TestSetStatus(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "statusbot", nil)

	if err := m.SetStatus("statusbot", bot.StatusRunning); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	state, _ := bot.LoadState(m.BotDir("statusbot"))
	if state.Status != bot.StatusRunning {
		t.Errorf("Status after set: %q", state.Status)
	}
}

func TestDeleteBot(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "delme", nil)

	if err := m.Delete("delme"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(m.BotDir("delme")); !os.IsNotExist(err) {
		t.Error("bot dir should not exist after delete")
	}
}

func TestDeleteBotNotFound(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.Delete("ghost"); err == nil {
		t.Error("expected error deleting non-existent bot")
	}
}

func TestIsStale(t *testing.T) {
	threshold := 2 * time.Minute

	notStale := &bot.Bot{State: &bot.BotState{Status: bot.StatusRunning, LastTickTS: time.Now().Unix()}}
	if notStale.IsStale(threshold) {
		t.Error("recent tick should not be stale")
	}

	stale := &bot.Bot{State: &bot.BotState{Status: bot.StatusRunning, LastTickTS: time.Now().Add(-5 * time.Minute).Unix()}}
	if !stale.IsStale(threshold) {
		t.Error("old tick should be stale")
	}

	stopped := &bot.Bot{State: &bot.BotState{Status: bot.StatusStopped, LastTickTS: time.Now().Add(-5 * time.Minute).Unix()}}
	if stopped.IsStale(threshold) {
		t.Error("stopped bot should not be stale")
	}

	noTick := &bot.Bot{State: &bot.BotState{Status: bot.StatusRunning, LastTickTS: 0}}
	if noTick.IsStale(threshold) {
		t.Error("bot with no tick should not be flagged stale")
	}
}

// TestCreateBotAllFieldsPreserved verifies that every BotConfig field survives
// the Create → config.json → LoadConfig round-trip unchanged.
func TestCreateBotAllFieldsPreserved(t *testing.T) {
	m, _ := newTestManager(t)
	m.TemplateBytes = []byte("# template")

	cfg := &bot.BotConfig{
		Name:              "fullbot",
		Goal:              "do everything",
		Model:             "my-special-model",
		Thinking:          true,
		Brain:             "initial brain content",
		Workspace:         "myws",
		WorkspacePath:     "/data/myws",
		Scope:             bot.ScopeGateway,
		AllowedWorkspaces: []string{"ws2", "ws3"},
		Parent:            "parentbot",
		GossipSecret:      "s3cr3t",
	}

	if err := m.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := bot.LoadConfig(m.BotDir("fullbot"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	check := func(field, got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: got %q, want %q", field, got, want)
		}
	}
	check("Name", loaded.Name, "fullbot")
	check("Goal", loaded.Goal, "do everything")
	check("Model", loaded.Model, "my-special-model")
	check("Brain", loaded.Brain, "initial brain content")
	check("Workspace", loaded.Workspace, "myws")
	check("WorkspacePath", loaded.WorkspacePath, "/data/myws")
	check("Scope", loaded.Scope, bot.ScopeGateway)
	check("Parent", loaded.Parent, "parentbot")
	check("GossipSecret", loaded.GossipSecret, "s3cr3t")

	if !loaded.Thinking {
		t.Error("Thinking: got false, want true")
	}
	if len(loaded.AllowedWorkspaces) != 2 || loaded.AllowedWorkspaces[0] != "ws2" || loaded.AllowedWorkspaces[1] != "ws3" {
		t.Errorf("AllowedWorkspaces: got %v, want [ws2 ws3]", loaded.AllowedWorkspaces)
	}
}

// TestCreateBotThinkingFalsePreserved ensures Thinking:false (zero value) is
// not silently dropped or defaulted to true.
func TestCreateBotThinkingFalsePreserved(t *testing.T) {
	m, _ := newTestManager(t)
	m.TemplateBytes = []byte("# template")

	cfg := &bot.BotConfig{Name: "nothink", Goal: "think less", Model: "m", Thinking: false}
	if err := m.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := bot.LoadConfig(m.BotDir("nothink"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Thinking {
		t.Error("Thinking: got true, want false")
	}
}

// TestCreateBotModelRequired ensures a bot cannot be started without a model.
func TestCreateBotModelPreserved(t *testing.T) {
	m, _ := newTestManager(t)
	m.TemplateBytes = []byte("# template")

	const wantModel = "qwen/qwen3-235b-a22b"
	cfg := &bot.BotConfig{Name: "modelbot", Goal: "run", Model: wantModel}
	if err := m.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	loaded, err := bot.LoadConfig(m.BotDir("modelbot"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Model != wantModel {
		t.Errorf("Model: got %q, want %q", loaded.Model, wantModel)
	}
}

// TestCreateBotTemplateBytesWritten ensures TemplateBytes are written to bot.py
// when set, and the file content matches exactly.
func TestCreateBotTemplateBytesWritten(t *testing.T) {
	m, _ := newTestManager(t)
	want := []byte("# injected template\nprint('hello')\n")
	m.TemplateBytes = want

	cfg := &bot.BotConfig{Name: "tmplbot", Goal: "test", Model: "m"}
	if err := m.Create(cfg); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := os.ReadFile(m.BotScript("tmplbot"))
	if err != nil {
		t.Fatalf("read bot.py: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("bot.py content mismatch:\n  got  %q\n  want %q", got, want)
	}
}

// TestCreateBotAsDictContainsAllKeys verifies AsDict produces all keys that
// botcore.py reads from CONFIG at startup.
func TestCreateBotAsDictContainsAllKeys(t *testing.T) {
	cfg := &bot.BotConfig{
		Name:              "d",
		Goal:              "g",
		Model:             "m",
		Thinking:          true,
		Workspace:         "ws",
		WorkspacePath:     "/ws",
		Scope:             bot.ScopeIsolated,
		AllowedWorkspaces: []string{"other"},
		Parent:            "p",
		GossipSecret:      "sec",
	}
	d := cfg.AsDict()

	required := []string{
		"name", "goal", "model", "thinking", "brain",
		"workspace", "workspace_path", "scope", "allowed_workspaces",
		"parent", "gossip_secret", "seed_addrs",
	}
	for _, k := range required {
		if _, ok := d[k]; !ok {
			t.Errorf("AsDict missing key %q", k)
		}
	}

	// seed_addrs must be a non-nil slice so botcore.py can iterate it
	seeds, ok := d["seed_addrs"].([]string)
	if !ok || seeds == nil {
		t.Errorf("seed_addrs must be []string, got %T", d["seed_addrs"])
	}
}
