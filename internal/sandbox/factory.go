package sandbox

import (
	"fmt"
	"os"
)

// SandboxMode controls which Sandbox implementation is used.
type SandboxMode string

const (
	ModeBwrap SandboxMode = "bwrap"
	ModeNone  SandboxMode = "none"
	ModeAuto  SandboxMode = "" // try bwrap, fall back to none
)

// Config holds sandbox configuration read from environment variables.
type Config struct {
	Mode        SandboxMode
	ExtraMounts string // BOT_SHELL_MOUNTS value
}

// ConfigFromEnv reads sandbox configuration from environment variables.
func ConfigFromEnv() Config {
	raw := os.Getenv("BOT_SHELL_SANDBOX")
	var mode SandboxMode
	switch raw {
	case "bwrap":
		mode = ModeBwrap
	case "none", "false":
		mode = ModeNone
	default:
		mode = ModeAuto
	}
	return Config{
		Mode:        mode,
		ExtraMounts: os.Getenv("BOT_SHELL_MOUNTS"),
	}
}

// New creates the Sandbox implementation described by cfg.
// Returns (sandbox, warning, error):
//   - warning is non-empty when a fallback occurred (e.g. bwrap requested but unavailable)
//   - error is returned only for invalid configurations
func New(cfg Config) (Sandbox, string, error) {
	switch cfg.Mode {
	case ModeBwrap:
		sb, err := NewBwrapSandbox(cfg.ExtraMounts)
		if err != nil {
			return nil, "", fmt.Errorf("BOT_SHELL_SANDBOX=bwrap but bwrap is unavailable: %w", err)
		}
		return sb, "", nil

	case ModeNone:
		return &NoSandbox{}, "BOT_SHELL_SANDBOX=none: commands run without isolation", nil

	default: // ModeAuto
		sb, err := NewBwrapSandbox(cfg.ExtraMounts)
		if err == nil {
			return sb, "", nil
		}
		return &NoSandbox{}, "bwrap not available; running without sandbox isolation", nil
	}
}
