package sandbox

import (
	"context"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// sysDirs are system directories to bind-mount (read-only) inside the bwrap sandbox.
var sysDirs = []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/lib32", "/etc", "/tmp"}

// BwrapSandbox runs commands inside a Linux user namespace via bwrap.
// The bot directory is bind-mounted as the container root; system dirs are
// bind-mounted read-only. The workspace path (if any) is bound read-write.
type BwrapSandbox struct {
	bwrapPath    string
	extraMounts  []mountSpec // parsed from BOT_SHELL_MOUNTS
}

type mountSpec struct {
	readOnly      bool
	hostPath      string
	containerPath string
}

// NewBwrapSandbox creates a BwrapSandbox. Returns an error if bwrap is not found.
func NewBwrapSandbox(extraMountsEnv string) (*BwrapSandbox, error) {
	path, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bwrap not found in PATH: %w", err)
	}
	mounts := parseMounts(extraMountsEnv)
	return &BwrapSandbox{bwrapPath: path, extraMounts: mounts}, nil
}

func (b *BwrapSandbox) Available() bool { return b.bwrapPath != "" }
func (b *BwrapSandbox) Name() string    { return "bwrap" }

func (b *BwrapSandbox) Execute(ctx context.Context, opts ExecOptions) (*ExecResult, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}

	cwd := opts.CWD
	if cwd == "" {
		cwd = opts.BotDir
	}

	args := b.buildArgs(opts.Command, opts.BotDir, cwd, opts.WorkspacePath)
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, b.bwrapPath, args...)
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
		return nil, fmt.Errorf("bwrap exec: %w", err)
	}
	return result, nil
}

// buildArgs constructs the bwrap argument list.
// Exported for testing.
func (b *BwrapSandbox) buildArgs(command, botDir, cwd, workspacePath string) []string {
	innerCWD := toInnerCWD(botDir, cwd)

	args := []string{"--chdir", innerCWD, "--bind", botDir, "/"}

	for _, sysDir := range sysDirs {
		if _, err := os.Stat(sysDir); err != nil {
			continue
		}
		link, err := os.Readlink(sysDir)
		if err == nil && link != "" {
			args = append(args, "--symlink", link, sysDir)
		} else if sysDir == "/tmp" {
			args = append(args, "--bind", sysDir, sysDir)
		} else {
			args = append(args, "--ro-bind", sysDir, sysDir)
		}
	}

	args = append(args, "--proc", "/proc", "--dev", "/dev")

	for _, m := range b.extraMounts {
		if _, err := os.Stat(m.hostPath); err != nil {
			continue
		}
		flag := "--bind"
		if m.readOnly {
			flag = "--ro-bind"
		}
		args = append(args, flag, m.hostPath, m.containerPath)
	}

	if workspacePath != "" {
		if _, err := os.Stat(workspacePath); err == nil {
			args = append(args, "--bind", workspacePath, workspacePath)
		}
	}

	args = append(args, "--", "bash", "-c", command)
	return args
}

// toInnerCWD maps an absolute cwd to a path inside the bwrap container.
// The bot directory is bound as the container root (/), so paths inside it
// become relative to /. Paths outside fall back to /.
func toInnerCWD(botDir, cwd string) string {
	botDir = filepath.Clean(botDir)
	cwd = filepath.Clean(cwd)

	rel, err := filepath.Rel(botDir, cwd)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "/"
	}
	if rel == "." {
		return "/"
	}
	return "/" + rel
}

// parseMounts parses the BOT_SHELL_MOUNTS env var.
// Format: mode:host_path:container_path (comma-separated).
// mode is "ro" or "rw".
func parseMounts(env string) []mountSpec {
	var specs []mountSpec
	for _, part := range strings.Split(env, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.SplitN(part, ":", 3)
		if len(fields) != 3 {
			continue
		}
		specs = append(specs, mountSpec{
			readOnly:      fields[0] == "ro",
			hostPath:      fields[1],
			containerPath: fields[2],
		})
	}
	return specs
}
