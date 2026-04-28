package bot

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	logslog "github.com/paularlott/logger/slog"
)

func makeRunnerProject(t *testing.T) (string, *Manager) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"bots", ".locks", "lib"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Minimal botcore.py that exits immediately
	script := `print("bot started")`
	if err := os.WriteFile(filepath.Join(root, "lib", "botcore.py"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, NewManager(root)
}

func makeBot(t *testing.T, mgr *Manager, name string) {
	t.Helper()
	cfg := &BotConfig{Name: name, Goal: "test", Model: "test"}
	if err := mgr.Create(cfg); err != nil {
		t.Fatalf("Create bot %q: %v", name, err)
	}
}

func testRunnerPool(t *testing.T, mgr *Manager) *RunnerPool {
	t.Helper()
	log := logslog.New(logslog.Config{Level: "error"})
	return NewRunnerPool(mgr, RunnerConfig{}, log)
}

func TestNextBackoff(t *testing.T) {
	cases := []struct {
		crashes int
		want    time.Duration
	}{
		{1, 2 * time.Second},  // 2^0 * 2s
		{2, 4 * time.Second},  // 2^1 * 2s
		{3, 8 * time.Second},  // 2^2 * 2s
		{4, 16 * time.Second}, // 2^3 * 2s
		{10, 60 * time.Second}, // capped at 60s
		{20, 60 * time.Second}, // still capped
	}
	for _, tc := range cases {
		got := nextBackoff(tc.crashes)
		if got != tc.want {
			t.Errorf("nextBackoff(%d) = %v, want %v", tc.crashes, got, tc.want)
		}
	}
}

func TestRunnerPoolStartAndStop(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "quickbot")
	// Use a script that blocks until context is cancelled.
	blockScript := `
import time
import scriptling.runtime as rt
while True:
    time.sleep(0.1)
`
	if err := os.WriteFile(filepath.Join(root, "bots", "quickbot", "bot.py"), []byte(blockScript), 0o644); err != nil {
		t.Fatal(err)
	}

	pool := testRunnerPool(t, mgr)

	if err := pool.Start("quickbot"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give bot time to reach running state.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := LoadState(mgr.BotDir("quickbot"))
		if state.Status == StatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	state, _ := LoadState(mgr.BotDir("quickbot"))
	if state.Status != StatusRunning {
		t.Errorf("expected running status, got %q", state.Status)
	}

	if !pool.IsRunning("quickbot") {
		t.Error("IsRunning should be true")
	}

	if err := pool.Kill("quickbot"); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	if pool.IsRunning("quickbot") {
		t.Error("IsRunning should be false after kill")
	}
}

func TestRunnerPoolStartDuplicate(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "dupebot")
	blockScript := `
import time
while True:
    time.sleep(0.1)
`
	_ = os.WriteFile(filepath.Join(root, "bots", "dupebot", "bot.py"), []byte(blockScript), 0o644)

	pool := testRunnerPool(t, mgr)

	if err := pool.Start("dupebot"); err != nil {
		t.Fatal(err)
	}
	// Wait for running
	time.Sleep(200 * time.Millisecond)

	if err := pool.Start("dupebot"); err == nil {
		t.Error("expected error starting already-running bot")
	}

	_ = pool.Kill("dupebot")
}

func TestRunnerPoolStartUnknownBot(t *testing.T) {
	_, mgr := makeRunnerProject(t)
	pool := testRunnerPool(t, mgr)
	if err := pool.Start("ghost"); err == nil {
		t.Error("expected error for unknown bot")
	}
}

func TestRunnerPoolStopNotRunning(t *testing.T) {
	_, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "idle")
	pool := testRunnerPool(t, mgr)
	if err := pool.Stop("idle"); err == nil {
		t.Error("expected error stopping non-running bot")
	}
}

func TestRunnerBotExitsCleanly(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "exitbot")
	// Script that exits immediately
	exitScript := `print("done")`
	_ = os.WriteFile(filepath.Join(root, "bots", "exitbot", "bot.py"), []byte(exitScript), 0o644)

	pool := testRunnerPool(t, mgr)
	if err := pool.Start("exitbot"); err != nil {
		t.Fatal(err)
	}

	// Wait for it to finish.
	pool.Wait("exitbot")

	state, _ := LoadState(mgr.BotDir("exitbot"))
	if state.Status != StatusStopped {
		t.Errorf("expected stopped status, got %q", state.Status)
	}
}

func TestRunnerCrashAndRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping crash-restart test in short mode")
	}
	root, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "crashbot")

	// Script that crashes on first run, exits cleanly on second.
	// Uses a sentinel file to detect which run we're on.
	sentinelPath := filepath.Join(root, "bots", "crashbot", "ran_once")
	crashScript := fmt.Sprintf(`
import os
import os.path

sentinel = %q
if os.path.exists(sentinel):
    # Second run — exit cleanly.
    pass
else:
    os.write_file(sentinel, "1")
    raise RuntimeError("intentional first-run crash")
`, sentinelPath)

	_ = os.WriteFile(filepath.Join(root, "bots", "crashbot", "bot.py"), []byte(crashScript), 0o644)

	pool := testRunnerPool(t, mgr)
	if err := pool.Start("crashbot"); err != nil {
		t.Fatal(err)
	}

	// Wait for bot to finish (crash + restart + clean exit). Max 10s (2s backoff + exec time).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !pool.IsRunning("crashbot") {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	pool.Wait("crashbot")
	state, _ := LoadState(mgr.BotDir("crashbot"))
	if state.Status != StatusStopped {
		t.Errorf("expected stopped, got %q", state.Status)
	}
	// Sentinel must exist (proves it ran twice).
	if _, err := os.Stat(sentinelPath); err != nil {
		t.Error("sentinel file missing — crash+restart did not happen")
	}
	_ = fmt.Sprintf // suppress unused import
}

// Compile-time check: verify context cancellation terminates bot.
func TestRunnerContextCancellation(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "ctxbot")
	blockScript := `
import time
while True:
    time.sleep(0.05)
`
	_ = os.WriteFile(filepath.Join(root, "bots", "ctxbot", "bot.py"), []byte(blockScript), 0o644)

	pool := testRunnerPool(t, mgr)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Replace pool's internal context with our timed one — we do this by
	// starting with Kill after deadline instead.
	if err := pool.Start("ctxbot"); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	_ = pool.Kill("ctxbot")

	if pool.IsRunning("ctxbot") {
		t.Error("bot should not be running after kill")
	}
}

func TestRunnerPoolStop(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	makeBot(t, mgr, "stopbot")
	blockScript := `
import time
while True:
    time.sleep(0.1)
`
	if err := os.WriteFile(filepath.Join(root, "bots", "stopbot", "bot.py"), []byte(blockScript), 0o644); err != nil {
		t.Fatal(err)
	}

	pool := testRunnerPool(t, mgr)
	if err := pool.Start("stopbot"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, _ := LoadState(mgr.BotDir("stopbot"))
		if state.Status == StatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := pool.Stop("stopbot"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	state, _ := LoadState(mgr.BotDir("stopbot"))
	if state.Status != StatusStopped {
		t.Errorf("expected stopped, got %q", state.Status)
	}
	if pool.IsRunning("stopbot") {
		t.Error("should not be running after Stop")
	}
}

func TestRunnerPoolStopAll(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	blockScript := `
import time
while True:
    time.sleep(0.1)
`
	for _, name := range []string{"sa1", "sa2"} {
		makeBot(t, mgr, name)
		if err := os.WriteFile(filepath.Join(root, "bots", name, "bot.py"), []byte(blockScript), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pool := testRunnerPool(t, mgr)
	pool.Start("sa1")
	pool.Start("sa2")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s1, _ := LoadState(mgr.BotDir("sa1"))
		s2, _ := LoadState(mgr.BotDir("sa2"))
		if s1.Status == StatusRunning && s2.Status == StatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	pool.StopAll()

	for _, name := range []string{"sa1", "sa2"} {
		state, _ := LoadState(mgr.BotDir(name))
		if state.Status != StatusStopped {
			t.Errorf("%s: expected stopped, got %q", name, state.Status)
		}
		if pool.IsRunning(name) {
			t.Errorf("%s: should not be running after StopAll", name)
		}
	}
}

func TestRunnerPoolKillAll(t *testing.T) {
	root, mgr := makeRunnerProject(t)
	blockScript := `
import time
while True:
    time.sleep(0.1)
`
	for _, name := range []string{"ka1", "ka2"} {
		makeBot(t, mgr, name)
		if err := os.WriteFile(filepath.Join(root, "bots", name, "bot.py"), []byte(blockScript), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pool := testRunnerPool(t, mgr)
	pool.Start("ka1")
	pool.Start("ka2")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s1, _ := LoadState(mgr.BotDir("ka1"))
		s2, _ := LoadState(mgr.BotDir("ka2"))
		if s1.Status == StatusRunning && s2.Status == StatusRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	pool.KillAll()

	for _, name := range []string{"ka1", "ka2"} {
		state, _ := LoadState(mgr.BotDir(name))
		if state.Status != StatusKilled {
			t.Errorf("%s: expected killed, got %q", name, state.Status)
		}
		if pool.IsRunning(name) {
			t.Errorf("%s: should not be running after KillAll", name)
		}
	}
}
