package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"praxis/internal/config"
)

func defaultModel() string {
	cfg := config.Get()
	if cfg == nil {
		return os.Getenv("BOT_MODEL")
	}
	return cfg.Bot.Model
}

func defaultGlobalSecret() string {
	cfg := config.Get()
	if cfg == nil {
		return os.Getenv("BOT_GLOBAL_SECRET")
	}
	return cfg.Watchdog.Secret
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

func resolveModelsDir(raw, projectDir string) string {
	cfg := config.Get()
	if cfg != nil {
		return cfg.ModelsDirResolved(projectDir)
	}
	p := raw
	if p == "" {
		p = filepath.Join(projectDir, "models")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	return abs
}

func resolveWorkspace(projectDir, name string) (path, gossipSecret, defaultScope string) {
	cfg := config.Get()
	if cfg != nil {
		p, s, sc, ok := cfg.ResolveWorkspace(name)
		if ok {
			return p, s, sc
		}
	}
	return "", "", ""
}
