package sandbox

import (
	"os/exec"
	"testing"
)

func TestToInnerCWD(t *testing.T) {
	cases := []struct {
		botDir string
		cwd    string
		want   string
	}{
		{"/bots/mybot", "/bots/mybot", "/"},
		{"/bots/mybot", "/bots/mybot/src", "/src"},
		{"/bots/mybot", "/bots/mybot/src/pkg", "/src/pkg"},
		{"/bots/mybot", "/etc", "/"},           // outside botDir → /
		{"/bots/mybot", "/bots/other", "/"},    // sibling dir → /
		{"/bots/mybot/", "/bots/mybot/src", "/src"}, // trailing slash on botDir
	}
	for _, tc := range cases {
		got := toInnerCWD(tc.botDir, tc.cwd)
		if got != tc.want {
			t.Errorf("toInnerCWD(%q, %q) = %q, want %q", tc.botDir, tc.cwd, got, tc.want)
		}
	}
}

func TestParseMounts(t *testing.T) {
	cases := []struct {
		env      string
		wantLen  int
		wantRO   []bool
		wantHost []string
		wantCont []string
	}{
		{"", 0, nil, nil, nil},
		{"ro:/host/data:/data", 1, []bool{true}, []string{"/host/data"}, []string{"/data"}},
		{"rw:/scratch:/scratch", 1, []bool{false}, []string{"/scratch"}, []string{"/scratch"}},
		{
			"ro:/a:/ca,rw:/b:/cb",
			2,
			[]bool{true, false},
			[]string{"/a", "/b"},
			[]string{"/ca", "/cb"},
		},
		{"bad-entry", 0, nil, nil, nil},                     // no colons
		{"only:two", 0, nil, nil, nil},                      // only 2 fields
		{" ro:/a:/b , rw:/c:/d ", 2, nil, nil, nil},         // trimmed spaces
	}
	for _, tc := range cases {
		got := parseMounts(tc.env)
		if len(got) != tc.wantLen {
			t.Errorf("parseMounts(%q): got %d specs, want %d", tc.env, len(got), tc.wantLen)
			continue
		}
		for i := range got {
			if tc.wantRO != nil && got[i].readOnly != tc.wantRO[i] {
				t.Errorf("mount[%d].readOnly = %v, want %v", i, got[i].readOnly, tc.wantRO[i])
			}
			if tc.wantHost != nil && got[i].hostPath != tc.wantHost[i] {
				t.Errorf("mount[%d].hostPath = %q, want %q", i, got[i].hostPath, tc.wantHost[i])
			}
			if tc.wantCont != nil && got[i].containerPath != tc.wantCont[i] {
				t.Errorf("mount[%d].containerPath = %q, want %q", i, got[i].containerPath, tc.wantCont[i])
			}
		}
	}
}

func TestBwrapAvailability(t *testing.T) {
	_, err := exec.LookPath("bwrap")
	bwrapPresent := err == nil

	sb := &BwrapSandbox{bwrapPath: ""}
	if sb.Available() {
		t.Error("Available() should be false when bwrapPath is empty")
	}

	if bwrapPresent {
		sb2, err := NewBwrapSandbox("")
		if err != nil {
			t.Fatalf("NewBwrapSandbox: %v", err)
		}
		if !sb2.Available() {
			t.Error("Available() should be true when bwrap is installed")
		}
	} else {
		t.Skip("bwrap not installed; skipping availability test")
	}
}

func TestBwrapName(t *testing.T) {
	sb := &BwrapSandbox{}
	if sb.Name() != "bwrap" {
		t.Errorf("Name() = %q, want %q", sb.Name(), "bwrap")
	}
}

func TestBwrapBuildArgsContainsCommand(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, _ := NewBwrapSandbox("")
	args := sb.buildArgs("echo hello", "/bots/mybot", "/bots/mybot", "")

	// must end with -- bash -c <command>
	n := len(args)
	if n < 3 || args[n-3] != "--" || args[n-2] != "bash" || args[n-1] != "bash" {
		// just check bash -c is near the end
		found := false
		for i := 0; i < n-1; i++ {
			if args[i] == "bash" && args[i+1] == "-c" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("args should contain 'bash -c': %v", args)
		}
	}
	// command must be last arg
	if args[len(args)-1] != "echo hello" {
		t.Errorf("last arg should be command, got %q", args[len(args)-1])
	}
}

func TestBwrapBuildArgsRootBinding(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, _ := NewBwrapSandbox("")
	args := sb.buildArgs("true", "/bots/mybot", "/bots/mybot", "")

	// --bind botDir / must be present
	found := false
	for i := 0; i < len(args)-2; i++ {
		if args[i] == "--bind" && args[i+1] == "/bots/mybot" && args[i+2] == "/" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("args should contain '--bind /bots/mybot /': %v", args)
	}
}

func TestBwrapBuildArgsWorkspacePath(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}
	sb, _ := NewBwrapSandbox("")

	// workspace exists (use /tmp which always exists)
	args := sb.buildArgs("true", "/bots/mybot", "/bots/mybot", "/tmp")
	found := false
	for i := 0; i < len(args)-2; i++ {
		if args[i] == "--bind" && args[i+1] == "/tmp" && args[i+2] == "/tmp" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("workspace /tmp should be bound, args: %v", args)
	}
}

func TestNewBwrapSandboxNotFound(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err == nil {
		t.Skip("bwrap is installed; cannot test 'not found' path")
	}
	_, err := NewBwrapSandbox("")
	if err == nil {
		t.Error("expected error when bwrap is not in PATH")
	}
}
