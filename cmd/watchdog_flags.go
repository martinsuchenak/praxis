package cmd

import (
	"github.com/paularlott/cli"

	"praxis/internal/config"
)

func watchdogFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "port",
			Usage:   "Gossip listen port",
			EnvVars: []string{"BOT_WATCHDOG_PORT"},
		},
		&cli.StringFlag{
			Name:    "advertise",
			Usage:   "Gossip advertise address",
			EnvVars: []string{"BOT_WATCHDOG_ADDR"},
		},
		&cli.StringFlag{
			Name:    "seeds",
			Usage:   "Comma-separated seed peer addresses",
			EnvVars: []string{"BOT_SEED_ADDRS"},
		},
		&cli.StringFlag{
			Name:    "secret",
			Usage:   "Global gossip secret",
			EnvVars: []string{"BOT_GLOBAL_SECRET"},
		},
		&cli.StringFlag{
			Name:    "sandbox",
			Usage:   "Sandbox mode: auto|bwrap|none",
			EnvVars: []string{"BOT_SHELL_SANDBOX"},
		},
		&cli.StringFlag{
			Name:    "mounts",
			Usage:   "Extra sandbox mounts",
			EnvVars: []string{"BOT_SHELL_MOUNTS"},
		},
		&cli.StringFlag{
			Name:    "allowlist",
			Usage:   "Comma-separated shell command allowlist",
			EnvVars: []string{"BOT_SHELL_ALLOWLIST"},
		},
		&cli.BoolFlag{
			Name:    "auth-disabled",
			Usage:   "Disable gossip secret validation (dev mode)",
			EnvVars: []string{"BOT_AUTH_DISABLED"},
		},
		&cli.StringFlag{
			Name:    "models-dir",
			Usage:   "Directory containing .gguf model files",
			EnvVars: []string{"BOT_MODELS_DIR"},
		},
		&cli.StringFlag{
			Name:    "node-name",
			Usage:   "Human-readable node name",
			EnvVars: []string{"BOT_NODE_NAME"},
		},
		&cli.StringFlag{
			Name:    "multicast-addr",
			Usage:   "Multicast group address",
			EnvVars: []string{"BOT_MULTICAST_ADDR"},
		},
		&cli.IntFlag{
			Name:    "multicast-port",
			Usage:   "Multicast port",
			EnvVars: []string{"BOT_MULTICAST_PORT"},
		},
		&cli.StringFlag{
			Name:    "tsnet-hostname",
			Usage:   "Tailscale hostname for remote swarm connectivity",
			EnvVars: []string{"BOT_TSNET_HOSTNAME"},
		},
		&cli.StringFlag{
			Name:    "tsnet-dir",
			Usage:   "Tsnet state directory",
			EnvVars: []string{"BOT_TSNET_DIR"},
		},
		&cli.StringFlag{
			Name:    "tsnet-authkey",
			Usage:   "Tailscale auth key",
			EnvVars: []string{"BOT_TSNET_AUTHKEY", "TS_AUTHKEY"},
		},
		&cli.StringFlag{
			Name:    "tsnet-controlurl",
			Usage:   "Custom coordination server URL",
			EnvVars: []string{"BOT_TSNET_CONTROLURL", "TS_CONTROL_URL"},
		},
	}
}

func overlayWatchdogFlags(cfg *config.Config, cmd *cli.Command) *config.Config {
	overlay := *cfg

	if v := cmd.GetString("port"); v != "" {
		overlay.Watchdog.Port = v
	}
	if v := cmd.GetString("advertise"); v != "" {
		overlay.Watchdog.Advertise = v
	}
	if v := cmd.GetString("seeds"); v != "" {
		overlay.Watchdog.Seeds = parseCSVFlag(v)
	}
	if v := cmd.GetString("secret"); v != "" {
		overlay.Watchdog.Secret = v
	}
	if v := cmd.GetString("sandbox"); v != "" {
		overlay.Watchdog.Sandbox = v
	}
	if v := cmd.GetString("mounts"); v != "" {
		overlay.Watchdog.Mounts = v
	}
	if v := cmd.GetString("allowlist"); v != "" {
		overlay.Watchdog.Allowlist = parseCSVFlag(v)
	}
	if cmd.GetBool("auth-disabled") {
		overlay.Watchdog.AuthDisabled = true
	}
	if v := cmd.GetString("models-dir"); v != "" {
		overlay.Watchdog.ModelsDir = v
	}
	if v := cmd.GetString("node-name"); v != "" {
		overlay.Watchdog.NodeName = v
	}
	if v := cmd.GetString("multicast-addr"); v != "" {
		overlay.Watchdog.MulticastAddr = v
	}
	if v := cmd.GetInt("multicast-port"); v != 0 {
		overlay.Watchdog.MulticastPort = v
	}
	if v := cmd.GetString("tsnet-hostname"); v != "" {
		overlay.Tsnet.Hostname = v
	}
	if v := cmd.GetString("tsnet-dir"); v != "" {
		overlay.Tsnet.Dir = v
	}
	if v := cmd.GetString("tsnet-authkey"); v != "" {
		overlay.Tsnet.AuthKey = v
	}
	if v := cmd.GetString("tsnet-controlurl"); v != "" {
		overlay.Tsnet.ControlURL = v
	}

	return &overlay
}
