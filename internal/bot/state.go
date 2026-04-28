package bot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const stateFile = "state.json"

// Status values for BotState.Status.
const (
	StatusCreated  = "created"
	StatusStarting = "starting"
	StatusRunning  = "running"
	StatusStopping = "stopping"
	StatusStopped  = "stopped"
	StatusKilled   = "killed"
)

// BotState is written by the bot itself (and by praxis for lifecycle signals).
// It lives in the bot's allowed filesystem paths.
type BotState struct {
	Status       string                 `json:"status"`
	GossipAddr   string                 `json:"gossip_addr,omitempty"`
	Fitness      map[string]interface{} `json:"fitness,omitempty"`
	LastTickTS   int64                  `json:"last_tick_ts,omitempty"`
	LastActivity string                 `json:"last_activity,omitempty"`
	IsLeader     bool                   `json:"is_leader,omitempty"`
}

// LoadState reads state.json from botDir. Returns a zero-value state if the file
// does not exist (e.g. bot was just created before first run).
func LoadState(botDir string) (*BotState, error) {
	data, err := os.ReadFile(filepath.Join(botDir, stateFile))
	if os.IsNotExist(err) {
		return &BotState{Status: StatusCreated}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s BotState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &s, nil
}

// SaveState atomically writes state.json to botDir.
func SaveState(botDir string, s *BotState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return atomicWrite(filepath.Join(botDir, stateFile), data)
}

// TicksAlive returns the ticks_alive fitness counter, or 0 if absent.
func (s *BotState) TicksAlive() int64 {
	if s.Fitness == nil {
		return 0
	}
	switch v := s.Fitness["ticks_alive"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// Spawns returns the spawns fitness counter, or 0 if absent.
func (s *BotState) Spawns() int64 {
	if s.Fitness == nil {
		return 0
	}
	switch v := s.Fitness["spawns"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
