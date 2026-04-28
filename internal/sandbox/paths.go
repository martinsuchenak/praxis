package sandbox

import (
	"fmt"
	"path/filepath"
	"sort"
)

// AllowedPaths computes the filesystem paths a bot interpreter may access.
// Derived from botDir, botsDir, locksDir, and optionally workspacePath.
// workspacePath is only included when non-empty.
func AllowedPaths(botDir, botsDir, locksDir, workspacePath string) []string {
	paths := []string{
		filepath.Clean(botDir),
		filepath.Clean(botsDir),
		filepath.Clean(locksDir),
	}
	if workspacePath != "" {
		paths = append(paths, filepath.Clean(workspacePath))
	}
	return paths
}

// InheritPaths returns the intersection of parent and child requested paths,
// returning an error if child requests any path that is not in parent's set.
// An empty child slice returns the parent's full set (no further restriction).
func InheritPaths(parent, child []string) ([]string, error) {
	if len(child) == 0 {
		return parent, nil
	}
	parentSet := make(map[string]bool, len(parent))
	for _, p := range parent {
		parentSet[filepath.Clean(p)] = true
	}

	var result []string
	for _, p := range child {
		clean := filepath.Clean(p)
		if !parentSet[clean] {
			return nil, fmt.Errorf("path %q is not in parent's allowed set", p)
		}
		result = append(result, clean)
	}
	sort.Strings(result)
	return result, nil
}
