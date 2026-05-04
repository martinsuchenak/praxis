# Configuration

All configuration is via environment variables, loaded from a `.env` file in the project directory (or exported directly).

Copy `.env.example` to `.env` and fill in at minimum `BOT_API_KEY`, `BOT_BASE_URL`, and `BOT_MODEL`.

## Required

| Variable | Description |
|---|---|
| `BOT_API_KEY` | API key for your LLM provider |
| `BOT_BASE_URL` | OpenAI-compatible endpoint URL |
| `BOT_MODEL` | Default model name (e.g. `qwen/qwen3-235b-a22b`) |

## Watchdog / Cluster

| Variable | Default | Description |
|---|---|---|
| `BOT_WATCHDOG_PORT` | `7700` | Gossip listen port for the watchdog node |
| `BOT_WATCHDOG_ADDR` | `0.0.0.0:<port>` | Gossip advertise address (override when behind NAT) |
| `BOT_SEED_ADDRS` | — | Comma-separated seed peer addresses for cluster bootstrap |
| `BOT_GLOBAL_SECRET` | — | Global gossip authentication secret (fallback when no workspace secret) |
| `BOT_NODE_NAME` | advertise address | Human-readable name for this watchdog node (used for remote spawn targeting) |
| `BOT_MULTICAST_ADDR` | `239.255.13.37` | Multicast group for auto-discovery (only used when no seeds configured) |
| `BOT_MULTICAST_PORT` | `19373` | Multicast port for auto-discovery |

## Bot Runtime

| Variable | Default | Description |
|---|---|---|
| `BOT_TICK_INTERVAL` | `30` | Seconds between ticks |
| `BOT_SCRIPT_TIMEOUT` | `30` | Scriptling script execution timeout in seconds |
| `BOT_TICK_MAX_ITERATIONS` | `5` | Max tool-call rounds per tick |
| `BOT_MAX_BACKOFF` | `600` | Max backoff seconds after repeated tick errors |
| `BOT_STALE_THRESHOLD` | `120` | Seconds before a running bot is flagged STALE |
| `BOT_MAX_CONCURRENT` | `1` | Max concurrent LLM calls (global fallback; override per-model in `models.json`) |

## Logging

| Variable | Default | Description |
|---|---|---|
| `BOT_LOG_VERBOSE` | `false` | Disable log truncation (full tool results logged) |
| `BOT_LOG_RESULT_MAX` | `80` | Max characters of tool result shown per log line |
| `LOG_LEVEL` | `info` | Praxis log level: `trace`, `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `console` | Praxis log format: `console` or `json` |

## Shell Sandbox

| Variable | Default | Description |
|---|---|---|
| `BOT_SHELL_SANDBOX` | `auto` | Sandbox mode: `auto` (use bwrap if available), `bwrap`, or `none` |
| `BOT_SHELL_MOUNTS` | — | Extra sandbox mounts: `mode:host_path:container_path` (comma-separated). Example: `ro:/data:/data,rw:/tmp/scratch:/scratch` |
| `BOT_SHELL_ALLOWLIST` | — | Comma-separated executables bots may run via `shell` (default: unrestricted except `curl`/`wget`) |
| `BOT_HTTP_ALLOWLIST` | — | Comma-separated domains bots may call via `http_request` (default: unrestricted) |

## Local Models (GGUF)

| Variable | Default | Description |
|---|---|---|
| `BOT_MODELS_DIR` | `<project_dir>/models` | Directory containing `.gguf` model files. If set and the directory exists, local inference via `local_generate` is available to all bots. |

## Praxis

| Variable | Default | Description |
|---|---|---|
| `PRAXIS_DIR` | `.` | Praxis project directory (equivalent to `--dir`) |

## Notes

- API keys are never written into bot source files. Bots read `BOT_API_KEY` and `BOT_BASE_URL` from the environment at runtime.
- `BOT_IP` is set automatically by the watchdog when launching bots — bots cannot detect their own IP.
- Per-workspace `gossip_secret` in `workspaces.json` overrides `BOT_GLOBAL_SECRET` for bots in that workspace.
- **Auto-discovery**: When `BOT_SEED_ADDRS` is not configured, watchdogs use UDP multicast (`239.255.13.37:19373`) to discover each other on the local network. Bots also use this same multicast channel. To disable auto-discovery, provide explicit seeds or set `BOT_MULTICAST_ADDR=""`.
