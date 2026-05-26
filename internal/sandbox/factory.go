package sandbox

import (
	"fmt"
	"os"
	"runtime"
)

type SandboxMode string

const (
	ModeBwrap       SandboxMode = "bwrap"
	ModeSandboxExec SandboxMode = "sandbox-exec"
	ModeNone        SandboxMode = "none"
	ModeAuto        SandboxMode = ""
)

type Config struct {
	Mode        SandboxMode
	ExtraMounts string
}

func ConfigFromEnv() Config {
	raw := os.Getenv("BOT_SHELL_SANDBOX")
	var mode SandboxMode
	switch raw {
	case "bwrap":
		mode = ModeBwrap
	case "sandbox-exec":
		mode = ModeSandboxExec
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

func New(cfg Config) (Sandbox, string, error) {
	switch cfg.Mode {
	case ModeBwrap:
		sb, err := NewBwrapSandbox(cfg.ExtraMounts)
		if err != nil {
			return nil, "", fmt.Errorf("BOT_SHELL_SANDBOX=bwrap but bwrap is unavailable: %w", err)
		}
		return sb, "", nil

	case ModeSandboxExec:
		sb, err := NewSandboxExecSandbox()
		if err != nil {
			return nil, "", fmt.Errorf("BOT_SHELL_SANDBOX=sandbox-exec but sandbox-exec is unavailable: %w", err)
		}
		return sb, "", nil

	case ModeNone:
		return &NoSandbox{}, "BOT_SHELL_SANDBOX=none: commands run without isolation", nil

	default: // ModeAuto
		if runtime.GOOS == "darwin" {
			sb, err := NewSandboxExecSandbox()
			if err == nil {
				return sb, "", nil
			}
		}
		sb, err := NewBwrapSandbox(cfg.ExtraMounts)
		if err == nil {
			return sb, "", nil
		}
		return &NoSandbox{}, "no sandbox available; running without isolation", nil
	}
}
