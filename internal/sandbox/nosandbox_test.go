package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNoSandboxAvailable(t *testing.T) {
	s := &NoSandbox{}
	if !s.Available() {
		t.Error("NoSandbox should always be available")
	}
}

func TestNoSandboxName(t *testing.T) {
	s := &NoSandbox{}
	if s.Name() != "none" {
		t.Errorf("Name() = %q, want %q", s.Name(), "none")
	}
}

func TestNoSandboxExecute(t *testing.T) {
	s := &NoSandbox{}
	result, err := s.Execute(context.Background(), ExecOptions{
		Command: "echo hello",
		BotDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("Stdout %q should contain 'hello'", result.Stdout)
	}
}

func TestNoSandboxNonZeroExit(t *testing.T) {
	s := &NoSandbox{}
	result, err := s.Execute(context.Background(), ExecOptions{
		Command: "exit 42",
		BotDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestNoSandboxStderr(t *testing.T) {
	s := &NoSandbox{}
	result, err := s.Execute(context.Background(), ExecOptions{
		Command: "echo error >&2",
		BotDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Stderr, "error") {
		t.Errorf("Stderr %q should contain 'error'", result.Stderr)
	}
}

func TestNoSandboxTimeout(t *testing.T) {
	s := &NoSandbox{}
	result, err := s.Execute(context.Background(), ExecOptions{
		Command: "sleep 60",
		BotDir:  t.TempDir(),
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("timed out command should have non-zero exit code")
	}
	if !strings.Contains(result.Stderr, "timed out") {
		t.Errorf("Stderr %q should mention timeout", result.Stderr)
	}
}

func TestNoSandboxUsesProvidedCWD(t *testing.T) {
	dir := t.TempDir()
	s := &NoSandbox{}
	result, err := s.Execute(context.Background(), ExecOptions{
		Command: "pwd",
		BotDir:  dir,
		CWD:     dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result.Stdout, dir) {
		t.Errorf("Stdout %q should contain CWD %q", result.Stdout, dir)
	}
}
