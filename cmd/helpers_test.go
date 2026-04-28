package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCSVFlag(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"single", []string{"single"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ", []string{"a", "b"}},
		{",,a,,", []string{"a"}},
	}
	for _, tc := range cases {
		got := parseCSVFlag(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseCSVFlag(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseCSVFlag(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

func TestResolveWorkspaceMissingFile(t *testing.T) {
	dir := t.TempDir()
	p, s, sc := resolveWorkspace(dir, "myapp")
	if p != "" || s != "" || sc != "" {
		t.Errorf("expected empty, got path=%q secret=%q scope=%q", p, s, sc)
	}
}

func TestResolveWorkspaceInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "workspaces.json"), []byte("{bad json"), 0o644)
	p, _, _ := resolveWorkspace(dir, "myapp")
	if p != "" {
		t.Error("expected empty for invalid JSON")
	}
}

func TestResolveWorkspaceNotFound(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "workspaces.json"), []byte(`{"other":"/path"}`), 0o644)
	p, _, _ := resolveWorkspace(dir, "myapp")
	if p != "" {
		t.Error("expected empty for missing workspace name")
	}
}

func TestResolveWorkspaceString(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "workspaces.json"),
		[]byte(`{"myapp":"/home/user/projects/myapp"}`), 0o644)
	p, s, sc := resolveWorkspace(dir, "myapp")
	if p != "/home/user/projects/myapp" {
		t.Errorf("path = %q", p)
	}
	if s != "" {
		t.Errorf("expected empty secret, got %q", s)
	}
	if sc != "" {
		t.Errorf("expected empty scope, got %q", sc)
	}
}

func TestResolveWorkspaceObject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "workspaces.json"), []byte(`{
		"myapp": {
			"path": "/home/user/projects/myapp",
			"gossip_secret": "s3cr3t",
			"default_scope": "isolated"
		}
	}`), 0o644)
	p, s, sc := resolveWorkspace(dir, "myapp")
	if p != "/home/user/projects/myapp" {
		t.Errorf("path = %q", p)
	}
	if s != "s3cr3t" {
		t.Errorf("secret = %q", s)
	}
	if sc != "isolated" {
		t.Errorf("scope = %q", sc)
	}
}

func TestResolveWorkspaceObjectPartial(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "workspaces.json"), []byte(`{
		"minimal": {
			"path": "/tmp/min"
		}
	}`), 0o644)
	p, s, sc := resolveWorkspace(dir, "minimal")
	if p != "/tmp/min" {
		t.Errorf("path = %q", p)
	}
	if s != "" {
		t.Errorf("secret = %q, want empty", s)
	}
	if sc != "" {
		t.Errorf("scope = %q, want empty", sc)
	}
}

func TestParseWorkspaceMappings(t *testing.T) {
	cases := []struct {
		input string
		want  map[string]string
	}{
		{"", map[string]string{}},
		{"myapp=/home/user/myapp", map[string]string{"myapp": "/home/user/myapp"}},
		{
			"a=/path/a,b=/path/b",
			map[string]string{"a": "/path/a", "b": "/path/b"},
		},
		{" noeq ", map[string]string{}},
		{"x=", map[string]string{"x": ""}},
		{" a = /path/a ", map[string]string{"a": "/path/a"}},
	}
	for _, tc := range cases {
		got := parseWorkspaceMappings(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseWorkspaceMappings(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("parseWorkspaceMappings(%q)[%q] = %q, want %q", tc.input, k, got[k], v)
			}
		}
	}
}
