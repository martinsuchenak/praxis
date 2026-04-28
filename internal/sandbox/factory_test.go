package sandbox

import (
	"os"
	"os/exec"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	cases := []struct {
		envVal   string
		wantMode SandboxMode
	}{
		{"bwrap", ModeBwrap},
		{"none", ModeNone},
		{"false", ModeNone},
		{"", ModeAuto},
		{"anything", ModeAuto},
	}
	for _, tc := range cases {
		t.Setenv("BOT_SHELL_SANDBOX", tc.envVal)
		cfg := ConfigFromEnv()
		if cfg.Mode != tc.wantMode {
			t.Errorf("BOT_SHELL_SANDBOX=%q: Mode=%q, want %q", tc.envVal, cfg.Mode, tc.wantMode)
		}
	}
}

func TestConfigFromEnvMounts(t *testing.T) {
	t.Setenv("BOT_SHELL_MOUNTS", "ro:/data:/data")
	cfg := ConfigFromEnv()
	if cfg.ExtraMounts != "ro:/data:/data" {
		t.Errorf("ExtraMounts = %q", cfg.ExtraMounts)
	}
}

func TestNewModeNone(t *testing.T) {
	sb, warn, err := New(Config{Mode: ModeNone})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sb.Name() != "none" {
		t.Errorf("Name() = %q, want none", sb.Name())
	}
	if warn == "" {
		t.Error("ModeNone should produce a warning")
	}
}

func TestNewModeBwrapWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, warn, err := New(Config{Mode: ModeBwrap})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sb.Name() != "bwrap" {
		t.Errorf("Name() = %q, want bwrap", sb.Name())
	}
	if warn != "" {
		t.Errorf("unexpected warning: %s", warn)
	}
}

func TestNewModeBwrapNotFound(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err == nil {
		t.Skip("bwrap is installed; cannot test not-found path")
	}
	_, _, err := New(Config{Mode: ModeBwrap})
	if err == nil {
		t.Error("expected error when bwrap is unavailable")
	}
}

func TestNewModeAutoFallsBackToNone(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err == nil {
		t.Skip("bwrap is installed; auto would pick bwrap")
	}
	sb, warn, err := New(Config{Mode: ModeAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sb.Name() != "none" {
		t.Errorf("Name() = %q, want none", sb.Name())
	}
	if warn == "" {
		t.Error("auto-fallback should produce a warning")
	}
}

func TestNewModeAutoPicksBwrap(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, warn, err := New(Config{Mode: ModeAuto})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sb.Name() != "bwrap" {
		t.Errorf("Name() = %q, want bwrap", sb.Name())
	}
	_ = warn
}

func TestConfigFromEnvCleansEnv(t *testing.T) {
	_ = os.Unsetenv("BOT_SHELL_SANDBOX")
	_ = os.Unsetenv("BOT_SHELL_MOUNTS")
	cfg := ConfigFromEnv()
	if cfg.Mode != ModeAuto {
		t.Errorf("unset env should produce ModeAuto, got %q", cfg.Mode)
	}
	if cfg.ExtraMounts != "" {
		t.Errorf("unset env mounts should be empty, got %q", cfg.ExtraMounts)
	}
}
