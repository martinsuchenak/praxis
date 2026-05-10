package cmd

import (
	"os"
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
