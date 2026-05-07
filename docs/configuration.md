# Configuration

Configuration is via TOML files with optional environment variable overrides. Resolution order: CLI flags > env vars > `praxis.toml` > `~/.config/praxis/config.toml` > defaults.

## Quick Start

```bash
# Initialize a project config
praxis init /path/to/project

# Or initialize global config
praxis init
```

This creates a `praxis.toml` with sensible defaults. Edit it to set your API key and model.

## Config File

Project-level config lives at `praxis.toml` in the project directory. Global config at `~/.config/praxis/config.toml` (respects `PRAXIS_HOME` and `XDG_CONFIG_HOME`). Both are loaded and merged — project config overrides global.

```toml
[watchdog]
port = "7700"
sandbox = "auto"

[bot]
base_url = "https://api.openai.com/v1"
model = "gpt-4o"
api_key = "sk-..."
tick_interval = 30
tick_max_iterations = 5
```

## Sections

### `[watchdog]`

| Key | Default | Env Override | Description |
|---|---|---|---|
| `port` | `"7700"` | `BOT_WATCHDOG_PORT` | Gossip listen port |
| `advertise` | `0.0.0.0:<port>` | `BOT_WATCHDOG_ADDR` | Gossip advertise address |
| `seeds` | `[]` | `BOT_SEED_ADDRS` | Gossip seed peers (comma-separated in env) |
| `secret` | `""` | `BOT_GLOBAL_SECRET` | Global gossip auth secret |
| `sandbox` | `"auto"` | `BOT_SHELL_SANDBOX` | Sandbox mode: `auto`, `bwrap`, `none` |
| `mounts` | `""` | `BOT_SHELL_MOUNTS` | Extra bwrap mounts |
| `allowlist` | `[]` | `BOT_SHELL_ALLOWLIST` | Shell command allowlist |
| `auth_disabled` | `false` | `BOT_AUTH_DISABLED` | Disable secret validation (dev mode) |
| `node_name` | `""` | `BOT_NODE_NAME` | Human-readable node name |
| `multicast_addr` | `""` | `BOT_MULTICAST_ADDR` | Multicast group (default: `239.255.13.37`) |
| `multicast_port` | `19373` | `BOT_MULTICAST_PORT` | Multicast port |
| `models_dir` | `""` | `BOT_MODELS_DIR` | Directory with `.gguf` files for local inference |

### `[tsnet]`

| Key | Default | Env Override | Description |
|---|---|---|---|
| `hostname` | `""` | `BOT_TSNET_HOSTNAME` | Enable tsnet remote connectivity |
| `dir` | `""` | `BOT_TSNET_DIR` | Tsnet state directory (default: `<project>/.tsnet`) |
| `authkey` | `""` | `BOT_TSNET_AUTHKEY`, `TS_AUTHKEY` | Tailscale auth key |
| `control_url` | `""` | `BOT_TSNET_CONTROLURL`, `TS_CONTROL_URL` | Custom coordination server |

### `[bot]`

| Key | Default | Env Override | Description |
|---|---|---|---|
| `base_url` | `""` | `BOT_BASE_URL` | LLM API base URL |
| `model` | `""` | `BOT_MODEL` | Default model ID |
| `api_key` | `""` | `BOT_API_KEY` | LLM API key |
| `tick_interval` | `30` | `BOT_TICK_INTERVAL` | Seconds between ticks |
| `tick_max_iterations` | `5` | `BOT_TICK_MAX_ITERATIONS` | Max tool-call rounds per tick |
| `log_verbose` | `false` | `BOT_LOG_VERBOSE` | Disable log truncation |
| `log_result_max` | `80` | `BOT_LOG_RESULT_MAX` | Max chars of tool result per log line |
| `stale_threshold` | `120` | `BOT_STALE_THRESHOLD` | Seconds before STALE flag |
| `script_timeout` | `30` | `BOT_SCRIPT_TIMEOUT` | Shell command timeout |
| `max_backoff` | `600` | `BOT_MAX_BACKOFF` | Max backoff after tick errors |
| `max_concurrent` | `1` | `BOT_MAX_CONCURRENT` | Max concurrent LLM calls |
| `http_allowlist` | `""` | `BOT_HTTP_ALLOWLIST` | Allowed HTTP domains |
| `shell_allowlist` | `""` | `BOT_SHELL_ALLOWLIST` | Allowed shell commands |
| `gossip_secret` | `""` | `BOT_GOSSIP_SECRET` | Bot-level gossip secret |
| `stuck_ticks` | `5` | `BOT_STUCK_TICKS` | Ticks before stuck detection |

### `[models]`

| Key | Description |
|---|---|
| `default` | Default model ID (overrides `[bot].model` if set) |

### `[[models.catalog]]`

| Key | Required | Description |
|---|---|---|
| `id` | yes | Model name as accepted by your API endpoint |
| `label` | yes | Human-readable name |
| `description` | yes | What the model is good for |
| `cost` | yes | `low`, `medium`, or `high` |
| `strengths` | yes | Tag list for model selection |
| `concurrency` | no | Max simultaneous LLM calls for this model |
| `thinking_template` | no | Built-in thinking template: `qwen`, `ollama`, `openai`, `anthropic`, `glm`, `gemini_flash`, `mistral` |
| `base_url` | no | Per-model API base URL override |
| `api_key` | no | Per-model API key override |

### `[[workspace]]`

| Key | Required | Description |
|---|---|---|
| `name` | yes | Workspace identifier |
| `path` | yes | Absolute host path to the project directory |
| `secret` | no | Workspace-specific gossip secret (overrides global) |
| `scope` | no | Default communication scope: `open`, `isolated`, `family`, `gateway` |
| `allow_cross` | no | Allow cross-workspace access (default: `false`) |

## Local Models (GGUF)

Set `models_dir` in `[watchdog]` to a directory containing `.gguf` files. If empty, defaults to `<project_dir>/models`. Download bundled models:

```bash
task models:download
```

## Notes

- **Secrets handling**: `api_key`, `base_url`, and `gossip_secret` are set as process environment variables from the TOML config. The bot reads them via `os.environ.get()` at runtime — they are never injected into the bot's CONFIG dict. This prevents accidental logging or exfiltration through the bot's self-inspection tools. `base_url` is included because the bot needs it for API calls but it's not sensitive.
- **Bot-accessible config**: Non-sensitive runtime settings (`tick_interval`, `max_concurrent`, `log_verbose`, etc.) are injected into the bot's CONFIG dict and readable by the bot script.
- `BOT_IP` is set automatically by the watchdog when launching bots — bots cannot detect their own IP.
- Per-workspace `secret` in `[[workspace]]` overrides the global `[bot].gossip_secret` for bots in that workspace. The workspace secret is injected into the bot's CONFIG as `gossip_secret` at spawn time (the bot needs it to sign gossip messages).
- **Auto-discovery**: When no seeds are configured, watchdogs use UDP multicast (`239.255.13.37:19373`) to discover each other. To disable, provide explicit seeds or set `multicast_addr = ""`.
- **Resolution order**: CLI flags > env vars > `praxis.toml` > `~/.config/praxis/config.toml` > built-in defaults. All env vars from the table above can be used alongside or instead of TOML keys.
- **`praxis init`**: Creates a `praxis.toml` with defaults. With a path argument creates a project config, without creates the global config.
