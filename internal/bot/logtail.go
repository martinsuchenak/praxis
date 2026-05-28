package bot

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"
)

type LogTailer struct {
	mu      sync.Mutex
	offsets map[string]int64
	mgr     *Manager
	log     interface {
		Debug(msg string, keysAndValues ...any)
	}
	interval time.Duration
}

func NewLogTailer(mgr *Manager, log interface {
	Debug(msg string, keysAndValues ...any)
}) *LogTailer {
	return &LogTailer{
		offsets:  make(map[string]int64),
		mgr:      mgr,
		log:      log,
		interval: 500 * time.Millisecond,
	}
}

func (t *LogTailer) SetInterval(d time.Duration) {
	t.interval = d
}

func (t *LogTailer) Run(ctx context.Context, pool interface{ IsRunning(string) bool }) {
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.poll(pool)
		}
	}
}

func (t *LogTailer) poll(pool interface{ IsRunning(string) bool }) {
	bots, err := t.mgr.List()
	if err != nil {
		return
	}

	for _, b := range bots {
		if !pool.IsRunning(b.Config.Name) {
			t.mu.Lock()
			delete(t.offsets, b.Config.Name)
			t.mu.Unlock()
			continue
		}

		logPath := b.Dir + "/bot.log"
		t.tailFile(b.Config.Name, logPath)
	}
}

func (t *LogTailer) tailFile(name, path string) {
	t.mu.Lock()
	offset, ok := t.offsets[name]
	t.mu.Unlock()

	if !ok {
		if info, err := os.Stat(path); err == nil {
			offset = info.Size()
		}
		t.mu.Lock()
		t.offsets[name] = offset
		t.mu.Unlock()
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return
	}

	if info.Size() < offset {
		offset = 0
	}

	if info.Size() == offset {
		return
	}

	buf := make([]byte, 32*1024)
	var sb strings.Builder
	for {
		n, readErr := f.ReadAt(buf, offset)
		if n > 0 {
			sb.Write(buf[:n])
			offset += int64(n)
		}
		if readErr != nil || n == 0 {
			break
		}
	}

	t.mu.Lock()
	t.offsets[name] = offset
	t.mu.Unlock()

	if sb.Len() > 0 {
		lines := strings.TrimSuffix(sb.String(), "\n")
		for _, line := range strings.Split(lines, "\n") {
			if line != "" {
				t.log.Debug(line, "bot", name)
			}
		}
	}
}
