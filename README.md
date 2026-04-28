# Praxis

Autonomous, self-evolving bots powered by LLMs. Each bot is a single self-contained script that can reason, build capabilities, communicate with peers, spawn children, modify its own behavior, and migrate to other machines.

> Built on [Scriptling](https://github.com/paularlott/scriptling) — a Python-like scripting language with a Go backend.

> **Disclaimer:** This project is under constant development. Changes are frequently not backward-compatible. Bots can cause real damage — to files, services, or anything they have access to. Do not run in any environment where such risk is not acceptable. The author is not responsible for any damage caused.

## Quick Start

```bash
# 1. Copy and fill in config
cp .env.example .env
# set BOT_API_KEY, BOT_BASE_URL, BOT_MODEL

# 2. Spawn a bot
praxis spawn Explorer "Explore and map the codebase"

# 3. Start the watchdog (manages bots, proxies shell, handles gossip)
praxis watchdog

# or use the interactive TUI
praxis tui
```

## Documentation

- [Configuration](docs/configuration.md) — environment variables and `.env` setup
- [Commands](docs/commands.md) — all `praxis` CLI commands
- [Workspaces](docs/workspaces.md) — giving bots access to external project directories
- [Networking](docs/networking.md) — gossip cluster, discovery, message types, and communication scope
- [Evolution](docs/evolution.md) — brain layers, fitness tracking, spawn, crossover, and consensus
- [Tools](docs/tools.md) — full bot tool reference
- [Models](docs/models.md) — multi-model catalog setup
- [Architecture](docs/architecture.md) — file layout, security model, and design decisions
