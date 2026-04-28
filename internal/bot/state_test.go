package bot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	s := &BotState{
		Status:     StatusRunning,
		GossipAddr: "192.168.1.1:9000",
		Fitness:    map[string]interface{}{"ticks_alive": float64(42)},
		IsLeader:   true,
	}

	if err := SaveState(dir, s); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.Status != StatusRunning {
		t.Errorf("Status: got %q want %q", loaded.Status, StatusRunning)
	}
	if loaded.GossipAddr != "192.168.1.1:9000" {
		t.Errorf("GossipAddr: got %q", loaded.GossipAddr)
	}
	if !loaded.IsLeader {
		t.Error("IsLeader should be true")
	}
}

func TestLoadStateMissingReturnsCreated(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Status != StatusCreated {
		t.Errorf("expected %q for missing state, got %q", StatusCreated, s.Status)
	}
}

func TestLoadStateBadJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFile), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState(dir)
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestSaveStateAtomic(t *testing.T) {
	dir := t.TempDir()
	s := &BotState{Status: StatusStopped}
	if err := SaveState(dir, s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, stateFile+".tmp")); !os.IsNotExist(err) {
		t.Error("tmp file should not remain after atomic write")
	}
}

func TestTicksAlive(t *testing.T) {
	cases := []struct {
		fitness map[string]interface{}
		want    int64
	}{
		{nil, 0},
		{map[string]interface{}{}, 0},
		{map[string]interface{}{"ticks_alive": float64(7)}, 7},
		{map[string]interface{}{"ticks_alive": int64(3)}, 3},
		{map[string]interface{}{"ticks_alive": int(5)}, 5},
		{map[string]interface{}{"ticks_alive": "bad"}, 0},
	}
	for _, tc := range cases {
		s := &BotState{Fitness: tc.fitness}
		if got := s.TicksAlive(); got != tc.want {
			t.Errorf("TicksAlive(%v) = %d, want %d", tc.fitness, got, tc.want)
		}
	}
}

func TestSpawns(t *testing.T) {
	s := &BotState{Fitness: map[string]interface{}{"spawns": float64(3)}}
	if got := s.Spawns(); got != 3 {
		t.Errorf("Spawns = %d, want 3", got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &BotState{
		Status:       StatusStopping,
		GossipAddr:   "10.0.0.1:8000",
		Fitness:      map[string]interface{}{"ticks_alive": float64(100), "spawns": float64(5)},
		LastTickTS:   1700000000,
		LastActivity: "did something",
		IsLeader:     false,
	}
	if err := SaveState(dir, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastTickTS != s.LastTickTS {
		t.Errorf("LastTickTS: got %d want %d", loaded.LastTickTS, s.LastTickTS)
	}
	if loaded.TicksAlive() != 100 {
		t.Errorf("TicksAlive after round-trip: %d", loaded.TicksAlive())
	}
}
