package sandbox

import (
	"context"
	"time"
)

// ExecOptions describes a shell command to be executed by a Sandbox.
type ExecOptions struct {
	Command       string
	BotDir        string
	CWD           string        // relative to BotDir; empty means BotDir
	WorkspacePath string        // mounted read-write when non-empty (bwrap only)
	Timeout       time.Duration // 0 means use a sensible default
}

// ExecResult holds the output of a sandboxed command.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Sandbox executes shell commands with configurable isolation.
// Implementations are chosen at startup and injected into handlers.
type Sandbox interface {
	// Execute runs opts.Command (via bash -c) with the configured isolation.
	Execute(ctx context.Context, opts ExecOptions) (*ExecResult, error)
	// Available returns false if this implementation cannot be used on the
	// current system (e.g. bwrap not installed).
	Available() bool
	// Name returns a short identifier used in log messages.
	Name() string
}
