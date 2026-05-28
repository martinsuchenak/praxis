package bot_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"praxis/internal/bot"
	"praxis/internal/testutil"
)

type mockDebugLogger struct {
	mu     sync.Mutex
	lines  []string
}

func (m *mockDebugLogger) Debug(msg string, keysAndValues ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lines = append(m.lines, msg)
}

func (m *mockDebugLogger) getLines() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.lines))
	copy(out, m.lines)
	return out
}

type mockPool struct {
	running map[string]bool
}

func (m *mockPool) IsRunning(id string) bool {
	return m.running[id]
}

func TestLogTailerStreamsNewLines(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "testbot", &bot.BotConfig{Name: "testbot", Goal: "test", Model: "m"})

	botDir := m.BotDir("testbot")
	logPath := filepath.Join(botDir, "bot.log")

	if err := os.WriteFile(logPath, []byte("[2026-01-01 00:00:00] initial line\n"), 0o644); err != nil {
		t.Fatalf("write initial log: %v", err)
	}

	logger := &mockDebugLogger{}
	tailer := bot.NewLogTailer(m, logger)
	tailer.SetInterval(50 * time.Millisecond)

	pool := &mockPool{running: map[string]bool{"testbot": true}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tailer.Run(ctx, pool)

	time.Sleep(100 * time.Millisecond)

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	_, _ = f.WriteString("[2026-01-01 00:00:01] tick #1\n[2026-01-01 00:00:02] tick #2\n")
	_ = f.Close()

	time.Sleep(200 * time.Millisecond)

	lines := logger.getLines()
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log lines, got %d: %v", len(lines), lines)
	}

	found1, found2 := false, false
	for _, l := range lines {
		if l == "[2026-01-01 00:00:01] tick #1" {
			found1 = true
		}
		if l == "[2026-01-01 00:00:02] tick #2" {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("expected both new log lines, got %v", lines)
	}
}

func TestLogTailerSkipsInitialContent(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "testbot", &bot.BotConfig{Name: "testbot", Goal: "test", Model: "m"})

	botDir := m.BotDir("testbot")
	logPath := filepath.Join(botDir, "bot.log")

	if err := os.WriteFile(logPath, []byte("[2026-01-01 00:00:00] old line\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	logger := &mockDebugLogger{}
	tailer := bot.NewLogTailer(m, logger)
	tailer.SetInterval(50 * time.Millisecond)

	pool := &mockPool{running: map[string]bool{"testbot": true}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tailer.Run(ctx, pool)

	time.Sleep(200 * time.Millisecond)

	lines := logger.getLines()
	if len(lines) != 0 {
		t.Errorf("expected no lines from pre-existing content, got %d: %v", len(lines), lines)
	}
}

func TestLogTailerSkipsStoppedBots(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "stopped", &bot.BotConfig{Name: "stopped", Goal: "test", Model: "m"})

	botDir := m.BotDir("stopped")
	logPath := filepath.Join(botDir, "bot.log")

	logger := &mockDebugLogger{}
	tailer := bot.NewLogTailer(m, logger)
	tailer.SetInterval(50 * time.Millisecond)

	pool := &mockPool{running: map[string]bool{}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tailer.Run(ctx, pool)

	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(logPath, []byte("[2026-01-01 00:00:00] new line\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	lines := logger.getLines()
	if len(lines) != 0 {
		t.Errorf("expected no lines for stopped bot, got %d: %v", len(lines), lines)
	}
}

func TestLogTailerStopsOnContext(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "testbot", &bot.BotConfig{Name: "testbot", Goal: "test", Model: "m"})

	logger := &mockDebugLogger{}
	tailer := bot.NewLogTailer(m, logger)
	tailer.SetInterval(10 * time.Millisecond)

	pool := &mockPool{running: map[string]bool{"testbot": true}}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		tailer.Run(ctx, pool)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("tailer did not stop after context cancellation")
	}
}

func TestLogTailerHandlesTruncatedLog(t *testing.T) {
	m, root := newTestManager(t)
	testutil.TempBot(t, root, "testbot", &bot.BotConfig{Name: "testbot", Goal: "test", Model: "m"})

	botDir := m.BotDir("testbot")
	logPath := filepath.Join(botDir, "bot.log")

	if err := os.WriteFile(logPath, []byte("[2026-01-01 00:00:00] line 1\n[2026-01-01 00:00:01] line 2\n[2026-01-01 00:00:02] line 3\n"), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}

	logger := &mockDebugLogger{}
	tailer := bot.NewLogTailer(m, logger)
	tailer.SetInterval(50 * time.Millisecond)

	pool := &mockPool{running: map[string]bool{"testbot": true}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tailer.Run(ctx, pool)
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(logPath, []byte("[2026-01-01 00:00:03] new after truncate\n"), 0o644); err != nil {
		t.Fatalf("truncate log: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	lines := logger.getLines()
	if len(lines) == 0 {
		t.Error("expected at least one line after truncation")
	}
	found := false
	for _, l := range lines {
		if l == "[2026-01-01 00:00:03] new after truncate" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected truncated log line, got %v", lines)
	}
}
