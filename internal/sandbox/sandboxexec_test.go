//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxExecAvailability(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}
	if !sb.Available() {
		t.Error("expected Available() = true")
	}
}

func TestSandboxExecName(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}
	if sb.Name() != "sandbox-exec" {
		t.Errorf("Name() = %q, want %q", sb.Name(), "sandbox-exec")
	}
}

func TestSandboxExecExecuteEcho(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}

	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := sb.Execute(t.Context(), ExecOptions{
		Command: "echo hello",
		BotDir:  botDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Errorf("Stdout = %q, want %q", strings.TrimSpace(result.Stdout), "hello")
	}
}

func TestSandboxExecExecuteNonZero(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}

	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := sb.Execute(t.Context(), ExecOptions{
		Command: "exit 42",
		BotDir:  botDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestSandboxExecExecuteWriteAllowed(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}

	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := sb.Execute(t.Context(), ExecOptions{
		Command: "touch testfile && echo ok",
		BotDir:  botDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
	}
	if _, err := os.Stat(filepath.Join(botDir, "testfile")); err != nil {
		t.Errorf("expected testfile to exist: %v", err)
	}
}

func TestSandboxExecExecuteWriteDenied(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}

	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := sb.Execute(t.Context(), ExecOptions{
		Command: "touch /sandbox_test_write_denied && echo ok",
		BotDir:  botDir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code when writing to denied path")
	}
}

func TestSandboxExecTimeout(t *testing.T) {
	sb, err := NewSandboxExecSandbox()
	if err != nil {
		t.Skipf("sandbox-exec not available: %v", err)
	}

	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := sb.Execute(t.Context(), ExecOptions{
		Command: "sleep 60",
		BotDir:  botDir,
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.ExitCode == 0 {
		t.Error("expected non-zero exit code on timeout")
	}
	if !strings.Contains(result.Stderr, "timed out") {
		t.Errorf("Stderr = %q, want timed out message", result.Stderr)
	}
}

func TestBuildSeatbeltProfile(t *testing.T) {
	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}
	wsPath := filepath.Join(tmp, "ws")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatal(err)
	}

	profile := buildSeatbeltProfile(botDir, wsPath)

	if !strings.Contains(profile, "(version 1)") {
		t.Error("profile missing version")
	}
	if !strings.Contains(profile, "(deny default)") {
		t.Error("profile missing deny default")
	}
	if !strings.Contains(profile, botDir) {
		t.Error("profile missing bot dir")
	}
	if !strings.Contains(profile, wsPath) {
		t.Error("profile missing workspace path")
	}
	if !strings.Contains(profile, "(allow network*)") {
		t.Error("profile missing network allow")
	}
	if !strings.Contains(profile, "(allow process-exec)") {
		t.Error("profile missing process-exec allow")
	}
	if !strings.Contains(profile, "(allow file-read*)") {
		t.Error("profile missing file-read wildcard")
	}
}

func TestBuildSeatbeltProfileNoWorkspace(t *testing.T) {
	tmp := t.TempDir()
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := buildSeatbeltProfile(botDir, "")
	if strings.Contains(profile, "workspace") {
		t.Error("profile should not mention workspace when empty")
	}
}

func TestBuildSeatbeltProfileWithWorkspace(t *testing.T) {
	tmp := t.TempDir()
	wsPath := filepath.Join(tmp, "ws")
	if err := os.MkdirAll(wsPath, 0755); err != nil {
		t.Fatal(err)
	}
	botDir := filepath.Join(tmp, "Bots", "testbot")
	if err := os.MkdirAll(botDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := buildSeatbeltProfile(botDir, wsPath)
	if !strings.Contains(profile, wsPath) {
		t.Errorf("profile missing workspace path %q", wsPath)
	}
}
