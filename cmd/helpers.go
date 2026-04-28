package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func defaultModel() string {
	return os.Getenv("BOT_MODEL")
}

func defaultGlobalSecret() string {
	return os.Getenv("BOT_GLOBAL_SECRET")
}

func parseCSVFlag(val string) []string {
	if val == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(val, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// resolveWorkspace looks up a workspace name in workspaces.json and returns
// (path, gossipSecret, defaultScope). Returns empty strings if not found.
func resolveWorkspace(projectDir, name string) (path, gossipSecret, defaultScope string) {
	data, err := os.ReadFile(filepath.Join(projectDir, "workspaces.json"))
	if err != nil {
		return
	}
	var workspaces map[string]interface{}
	if err := json.Unmarshal(data, &workspaces); err != nil {
		return
	}
	entry, ok := workspaces[name]
	if !ok {
		return
	}
	switch v := entry.(type) {
	case string:
		path = v
	case map[string]interface{}:
		if p, ok := v["path"].(string); ok {
			path = p
		}
		if s, ok := v["gossip_secret"].(string); ok {
			gossipSecret = s
		}
		if ds, ok := v["default_scope"].(string); ok {
			defaultScope = ds
		}
	}
	return
}
