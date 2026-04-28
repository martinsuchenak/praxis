package sandbox

import (
	"testing"
)

func TestAllowedPathsNoWorkspace(t *testing.T) {
	paths := AllowedPaths("/bots/mybot", "/bots", "/.locks", "")
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d: %v", len(paths), paths)
	}
	want := map[string]bool{"/bots/mybot": true, "/bots": true, "/.locks": true}
	for _, p := range paths {
		if !want[p] {
			t.Errorf("unexpected path %q", p)
		}
	}
}

func TestAllowedPathsWithWorkspace(t *testing.T) {
	paths := AllowedPaths("/bots/mybot", "/bots", "/.locks", "/workspace/proj")
	if len(paths) != 4 {
		t.Fatalf("expected 4 paths, got %d", len(paths))
	}
	found := false
	for _, p := range paths {
		if p == "/workspace/proj" {
			found = true
		}
	}
	if !found {
		t.Error("workspace path not in allowed paths")
	}
}

func TestAllowedPathsCleansTrailingSlash(t *testing.T) {
	paths := AllowedPaths("/bots/mybot/", "/bots/", "/.locks/", "")
	for _, p := range paths {
		if p[len(p)-1] == '/' {
			t.Errorf("path %q should not have trailing slash", p)
		}
	}
}

func TestInheritPathsEmptyChild(t *testing.T) {
	parent := []string{"/bots/a", "/bots", "/.locks"}
	result, err := InheritPaths(parent, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(parent) {
		t.Errorf("empty child should return parent paths: got %v", result)
	}
}

func TestInheritPathsSubset(t *testing.T) {
	parent := []string{"/bots/a", "/bots", "/.locks", "/workspace"}
	child := []string{"/bots/a", "/.locks"}
	result, err := InheritPaths(parent, child)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 paths, got %d: %v", len(result), result)
	}
}

func TestInheritPathsUnauthorizedPath(t *testing.T) {
	parent := []string{"/bots/a", "/bots", "/.locks"}
	child := []string{"/bots/a", "/etc"}
	_, err := InheritPaths(parent, child)
	if err == nil {
		t.Error("expected error when child requests path outside parent set")
	}
}

func TestInheritPathsExactMatch(t *testing.T) {
	parent := []string{"/bots/a"}
	child := []string{"/bots/a"}
	result, err := InheritPaths(parent, child)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 || result[0] != "/bots/a" {
		t.Errorf("expected [/bots/a], got %v", result)
	}
}

func TestInheritPathsNormalizesTrailingSlash(t *testing.T) {
	parent := []string{"/bots/a"}
	child := []string{"/bots/a/"}
	_, err := InheritPaths(parent, child)
	if err != nil {
		t.Errorf("trailing slash should be normalized, got error: %v", err)
	}
}

func TestInheritPathsEmptyParent(t *testing.T) {
	_, err := InheritPaths([]string{}, []string{"/anything"})
	if err == nil {
		t.Error("expected error when parent has no paths and child requests one")
	}
}
