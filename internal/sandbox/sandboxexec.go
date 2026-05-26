//go:build darwin

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type SandboxExecSandbox struct {
	path string
}

func NewSandboxExecSandbox() (*SandboxExecSandbox, error) {
	path, err := exec.LookPath("sandbox-exec")
	if err != nil {
		return nil, fmt.Errorf("sandbox-exec not found in PATH: %w", err)
	}
	return &SandboxExecSandbox{path: path}, nil
}

func (s *SandboxExecSandbox) Available() bool { return s.path != "" }
func (s *SandboxExecSandbox) Name() string    { return "sandbox-exec" }

func (s *SandboxExecSandbox) Execute(ctx context.Context, opts ExecOptions) (*ExecResult, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	cwd := opts.CWD
	if cwd == "" {
		cwd = opts.BotDir
	}

	profile := buildSeatbeltProfile(opts.BotDir, opts.WorkspacePath)

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, s.path, "-p", profile, "--", "bash", "-c", opts.Command)
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
		result.ExitCode = 1
		result.Stderr = err.Error()
		return result, nil
	}
	return result, nil
}

func resolvePath(p string) string {
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

func sanitizeSBPLPath(p string) string {
	return strings.ReplaceAll(strings.ReplaceAll(p, `\`, `\\`), `"`, `\"`)
}

func buildSeatbeltProfile(botDir, workspacePath string) string {
	botDir = resolvePath(botDir)
	botsDir := filepath.Dir(botDir)
	locksDir := filepath.Join(filepath.Dir(botsDir), ".locks")

	var writePaths []string
	writePaths = append(writePaths, "(subpath \""+sanitizeSBPLPath(botDir)+"\")")
	if _, err := os.Stat(botsDir); err == nil {
		writePaths = append(writePaths, "(subpath \""+sanitizeSBPLPath(botsDir)+"\")")
	}
	if _, err := os.Stat(locksDir); err == nil {
		writePaths = append(writePaths, "(subpath \""+sanitizeSBPLPath(locksDir)+"\")")
	}
	writePaths = append(writePaths, "(subpath \"/private/tmp\")")
	writePaths = append(writePaths, "(subpath \"/var\")")
	if workspacePath != "" {
		wp := resolvePath(workspacePath)
		if _, err := os.Stat(wp); err == nil {
			writePaths = append(writePaths, "(subpath \""+sanitizeSBPLPath(wp)+"\")")
		}
	}

	profile := "(version 1)" +
		"(deny default)" +
		"(allow process-exec)" +
		"(allow process-fork)" +
		"(allow file-read*)" +
		"(allow file-write* " + strings.Join(writePaths, " ") + ")" +
		// network* is intentionally unrestricted — bots must reach external LLM APIs.
		// This sandbox provides filesystem containment only.
		"(allow network*)" +
		"(allow signal)" +
		"(allow sysctl-read)"

	return profile
}
