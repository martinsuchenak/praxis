package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Watchdog   WatchdogConfig   `toml:"watchdog"`
	Tsnet      TsnetConfig      `toml:"tsnet"`
	Bot        BotDefaults      `toml:"bot"`
	Workspaces []WorkspaceEntry `toml:"workspace"`
	Models     ModelsConfig     `toml:"models"`
}

type WatchdogConfig struct {
	Port          string   `toml:"port"`
	Advertise     string   `toml:"advertise"`
	Seeds         []string `toml:"seeds"`
	Secret        string   `toml:"secret"`
	Sandbox       string   `toml:"sandbox"`
	Mounts        string   `toml:"mounts"`
	Allowlist     []string `toml:"allowlist"`
	AuthDisabled  bool     `toml:"auth_disabled"`
	NodeName      string   `toml:"node_name"`
	MulticastAddr string   `toml:"multicast_addr"`
	MulticastPort int      `toml:"multicast_port"`
	ModelsDir     string   `toml:"models_dir"`
}

type TsnetConfig struct {
	Hostname   string `toml:"hostname"`
	Dir        string `toml:"dir"`
	AuthKey    string `toml:"authkey"`
	ControlURL string `toml:"control_url"`
}

type BotDefaults struct {
	BaseURL          string `toml:"base_url"`
	Model            string `toml:"model"`
	APIKey           string `toml:"api_key"`
	TickInterval     int    `toml:"tick_interval"`
	TickMaxIter      int    `toml:"tick_max_iterations"`
	LogVerbose       bool   `toml:"log_verbose"`
	LogResultMax     int    `toml:"log_result_max"`
	StaleThreshold   int    `toml:"stale_threshold"`
	ScriptTimeout    int    `toml:"script_timeout"`
	MaxBackoff       int    `toml:"max_backoff"`
	MaxConcurrent    int    `toml:"max_concurrent"`
	HTTPAllowlist    string `toml:"http_allowlist"`
	ShellAllowlist   string `toml:"shell_allowlist"`
	GossipSecret     string `toml:"gossip_secret"`
	StuckTicks       int    `toml:"stuck_ticks"`
}

type WorkspaceEntry struct {
	Name       string `toml:"name"`
	Path       string `toml:"path"`
	Secret     string `toml:"secret"`
	Scope      string `toml:"scope"`
	AllowCross bool   `toml:"allow_cross"`
}

type ModelsConfig struct {
	Default string        `toml:"default"`
	Catalog []ModelEntry  `toml:"catalog"`
}

type ModelEntry struct {
	ID               string                 `toml:"id"`
	Label            string                 `toml:"label"`
	Description      string                 `toml:"description"`
	Cost             string                 `toml:"cost"`
	Strengths        []string               `toml:"strengths"`
	Concurrency      int                    `toml:"concurrency"`
	ThinkingTemplate string                 `toml:"thinking_template"`
	BaseURL          string                 `toml:"base_url"`
	APIKey           string                 `toml:"api_key"`
}

var (
	globalCfg *Config
	globalMu  sync.RWMutex
)

func Get() *Config {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalCfg
}

func Set(cfg *Config) {
	globalMu.Lock()
	globalCfg = cfg
	globalMu.Unlock()
}

func GlobalDir() string {
	if d := os.Getenv("PRAXIS_HOME"); d != "" {
		return d
	}
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".config")
	}
	return filepath.Join(xdg, "praxis")
}

func Load(projectDir string) (*Config, error) {
	cfg := &Config{}
	setDefaults(cfg)

	paths := []string{
		filepath.Join(GlobalDir(), "config.toml"),
		filepath.Join(projectDir, "praxis.toml"),
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			if _, err := toml.DecodeFile(p, cfg); err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
		}
	}

	applyEnvOverrides(cfg)
	applySecretsToEnv(cfg)

	Set(cfg)
	return cfg, nil
}

func setDefaults(cfg *Config) {
	cfg.Watchdog.Port = "7700"
	cfg.Watchdog.Sandbox = "auto"
	cfg.Watchdog.MulticastPort = 19373
	cfg.Bot.TickInterval = 30
	cfg.Bot.TickMaxIter = 5
	cfg.Bot.LogResultMax = 80
	cfg.Bot.StaleThreshold = 120
	cfg.Bot.ScriptTimeout = 30
	cfg.Bot.MaxBackoff = 600
	cfg.Bot.MaxConcurrent = 1
	cfg.Bot.StuckTicks = 5
}

func applyEnvOverrides(cfg *Config) {
	envStr := func(key string, dest *string) {
		if v := os.Getenv(key); v != "" {
			*dest = v
		}
	}
	envInt := func(key string, dest *int) {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				*dest = n
			}
		}
	}
	envBool := func(key string, dest *bool) {
		if v := os.Getenv(key); v != "" {
			*dest = v == "true"
		}
	}
	envCSV := func(key string, dest *[]string) {
		if v := os.Getenv(key); v != "" {
			var out []string
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
			*dest = out
		}
	}

	envStr("BOT_WATCHDOG_PORT", &cfg.Watchdog.Port)
	envStr("BOT_WATCHDOG_ADDR", &cfg.Watchdog.Advertise)
	envStr("BOT_GLOBAL_SECRET", &cfg.Watchdog.Secret)
	envStr("BOT_SHELL_SANDBOX", &cfg.Watchdog.Sandbox)
	envStr("BOT_SHELL_MOUNTS", &cfg.Watchdog.Mounts)
	envCSV("BOT_SHELL_ALLOWLIST", &cfg.Watchdog.Allowlist)
	envBool("BOT_AUTH_DISABLED", &cfg.Watchdog.AuthDisabled)
	envStr("BOT_NODE_NAME", &cfg.Watchdog.NodeName)
	envStr("BOT_MULTICAST_ADDR", &cfg.Watchdog.MulticastAddr)
	envInt("BOT_MULTICAST_PORT", &cfg.Watchdog.MulticastPort)
	envStr("BOT_MODELS_DIR", &cfg.Watchdog.ModelsDir)
	if v := os.Getenv("BOT_SEED_ADDRS"); v != "" {
		cfg.Watchdog.Seeds = parseCSV(v)
	}

	envStr("BOT_TSNET_HOSTNAME", &cfg.Tsnet.Hostname)
	envStr("BOT_TSNET_DIR", &cfg.Tsnet.Dir)
	envStr("BOT_TSNET_AUTHKEY", &cfg.Tsnet.AuthKey)
	if v := os.Getenv("TS_AUTHKEY"); v != "" && cfg.Tsnet.AuthKey == "" {
		cfg.Tsnet.AuthKey = v
	}
	envStr("BOT_TSNET_CONTROLURL", &cfg.Tsnet.ControlURL)
	if v := os.Getenv("TS_CONTROL_URL"); v != "" && cfg.Tsnet.ControlURL == "" {
		cfg.Tsnet.ControlURL = v
	}

	envStr("BOT_BASE_URL", &cfg.Bot.BaseURL)
	envStr("BOT_MODEL", &cfg.Bot.Model)
	envStr("BOT_API_KEY", &cfg.Bot.APIKey)
	envInt("BOT_TICK_INTERVAL", &cfg.Bot.TickInterval)
	envInt("BOT_TICK_MAX_ITERATIONS", &cfg.Bot.TickMaxIter)
	envBool("BOT_LOG_VERBOSE", &cfg.Bot.LogVerbose)
	envInt("BOT_LOG_RESULT_MAX", &cfg.Bot.LogResultMax)
	envInt("BOT_STALE_THRESHOLD", &cfg.Bot.StaleThreshold)
	envInt("BOT_SCRIPT_TIMEOUT", &cfg.Bot.ScriptTimeout)
	envInt("BOT_MAX_BACKOFF", &cfg.Bot.MaxBackoff)
	envInt("BOT_MAX_CONCURRENT", &cfg.Bot.MaxConcurrent)
	envStr("BOT_HTTP_ALLOWLIST", &cfg.Bot.HTTPAllowlist)
	envStr("BOT_SHELL_ALLOWLIST", &cfg.Bot.ShellAllowlist)
	envStr("BOT_GOSSIP_SECRET", &cfg.Bot.GossipSecret)
	envInt("BOT_STUCK_TICKS", &cfg.Bot.StuckTicks)
}

func applySecretsToEnv(cfg *Config) {
	if cfg.Bot.APIKey != "" {
		os.Setenv("BOT_API_KEY", cfg.Bot.APIKey)
	}
	if cfg.Bot.BaseURL != "" {
		os.Setenv("BOT_BASE_URL", cfg.Bot.BaseURL)
	}
	if cfg.Bot.GossipSecret != "" {
		os.Setenv("BOT_GOSSIP_SECRET", cfg.Bot.GossipSecret)
	}
}

func parseCSV(s string) []string {
	var out []string
	for _, s := range strings.Split(s, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *Config) ResolveWorkspace(name string) (path, secret, scope string, ok bool) {
	for _, ws := range c.Workspaces {
		if ws.Name == name {
			return ws.Path, ws.Secret, ws.Scope, true
		}
	}
	return "", "", "", false
}

func (c *Config) SetWorkspace(entry WorkspaceEntry) {
	for i, ws := range c.Workspaces {
		if ws.Name == entry.Name {
			c.Workspaces[i] = entry
			return
		}
	}
	c.Workspaces = append(c.Workspaces, entry)
}

func (c *Config) RemoveWorkspace(name string) bool {
	for i, ws := range c.Workspaces {
		if ws.Name == name {
			c.Workspaces = append(c.Workspaces[:i], c.Workspaces[i+1:]...)
			return true
		}
	}
	return false
}

func (c *Config) Save(projectDir string) error {
	p := filepath.Join(projectDir, "praxis.toml")
	f, err := os.Create(p + ".tmp")
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(p+".tmp", p)
}

func (c *Config) ModelsAsInterface() []interface{} {
	var out []interface{}
	for _, m := range c.Models.Catalog {
		entry := map[string]interface{}{
			"id":          m.ID,
			"label":       m.Label,
			"description": m.Description,
			"cost":        m.Cost,
			"concurrency": m.Concurrency,
		}
		if len(m.Strengths) > 0 {
			entry["strengths"] = m.Strengths
		}
		if m.ThinkingTemplate != "" {
			entry["thinking_template"] = m.ThinkingTemplate
		}
		if m.BaseURL != "" {
			entry["base_url"] = m.BaseURL
		}
		if m.APIKey != "" {
			entry["api_key"] = m.APIKey
		}
		out = append(out, entry)
	}
	return out
}

func (c *Config) ClusterBindAddr() string {
	return "0.0.0.0:" + c.Watchdog.Port
}

func (c *Config) ClusterAdvertiseAddr() string {
	if c.Watchdog.Advertise != "" {
		return c.Watchdog.Advertise
	}
	return c.ClusterBindAddr()
}

func (c *Config) TsnetDirOrDefault(projectDir string) string {
	if c.Tsnet.Dir != "" {
		return c.Tsnet.Dir
	}
	return filepath.Join(projectDir, ".tsnet")
}

func InitDir(target string) (string, error) {
	if target == "" {
		target = GlobalDir()
	}

	abs, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}

	cfgPath := filepath.Join(abs, "config.toml")
	if _, err := os.Stat(cfgPath); err == nil {
		return "", fmt.Errorf("%s already exists", cfgPath)
	}

	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}

	cfg := &Config{}
	setDefaults(cfg)

	f, err := os.Create(cfgPath)
	if err != nil {
		return "", fmt.Errorf("create config: %w", err)
	}
	defer f.Close()

	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}

	return abs, nil
}

func (c *Config) ModelsDirResolved(projectDir string) string {
	p := c.Watchdog.ModelsDir
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
