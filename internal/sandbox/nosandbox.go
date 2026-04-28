package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// NoSandbox executes commands without any isolation.
// Used for development, testing, or systems where bwrap is unavailable.
// It logs a warning at startup when used in production mode.
type NoSandbox struct{}

func (n *NoSandbox) Available() bool { return true }
func (n *NoSandbox) Name() string    { return "none" }

func (n *NoSandbox) Execute(ctx context.Context, opts ExecOptions) (*ExecResult, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	cwd := opts.CWD
	if cwd == "" {
		cwd = opts.BotDir
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", opts.Command)
	cmd.Dir = cwd

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			result.ExitCode = 1
			result.Stderr = fmt.Sprintf("command timed out after %s", timeout)
			return result, nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return nil, fmt.Errorf("exec: %w", err)
	}
	return result, nil
}
