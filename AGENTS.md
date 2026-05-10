# AGENTS.md

## Commands

```bash
task build              # → bin/praxis (stripped release binary)
task test               # all tests
task test:unit          # -short only
task test:race          # race detector
task vet                # go vet
task lint               # golangci-lint (must be installed)
task check              # vet + lint + test:race (full CI gate)
task tidy               # go mod tidy + verify

# Direct
go test ./...                              # all tests
go test ./internal/cluster/                # single package
go test -run TestHandleSpawnReq ./internal/cluster/   # single test
```

Always run `go vet ./...` and `go build ./...` after changes. Run `task lint` if golangci-lint is available.

## Architecture

Single Go binary (`main.go`) + embedded Python bot template (`lib/botcore.py`).

**Entrypoint**: `cmd/root.go` builds the CLI tree. `main.go` embeds `lib/botcore.py` via `//go:embed` and passes it to `cmd.SetBotcoreTemplate`.

**Packages**:

| Package | Purpose |
|---|---|
| `cmd/` | CLI commands. Each `*Cmd()` function returns a `*cli.Command`. Shared state via `AppContext` in context. |
| `internal/bot/` | Bot config/state persistence (`config.go`, `state.go`), process runner via embedded scriptling (`runner.go`), bot manager (`manager.go`), export/import (`export.go`) |
| `internal/cluster/` | Gossip cluster node. Message dispatcher routes by `type` field. Handlers: `proxy.go` (shell_req), `spawn.go` (spawn_req), `relay.go` (relay_req), `remote_spawn.go` (remote_spawn_req), `terminate.go` (terminate_req), `hardware.go` (hardware_req), `multicast.go` (auto-discovery) |
| `internal/config/` | TOML config loading, env overrides, workspace/model resolution. `config.go` defines all structs (`Config`, `WatchdogConfig`, `BotDefaults`, `WorkspaceEntry`, `ModelEntry`). `Get()` returns the global config. `Load(projectDir)` reads `~/.config/praxis/config.toml` + `praxis.toml`, applies env overrides. |
| `internal/sandbox/` | Shell command sandboxing (bwrap or none). Interface in `sandbox.go`. |
| `internal/hooks/` | Lifecycle hook dispatcher. `Fire()` runs configured command/HTTP hooks for an event. |
| `internal/tui/` | Terminal UI dashboard (`dashboard.go`). All `/` commands are methods on `Dashboard`. |
| `internal/testutil/` | `MockSandbox`, `TempProject()`, `TempBot()` — use these in tests. |
| `lib/` | Python files embedded into bots at spawn. `botcore.py` is the bot runtime (tools, LLM loop, gossip). |

**Key relationships**:
- `AppContext` in `cmd/root.go` holds `*config.Config`, `*bot.Manager`, and logger. All subcommands access it via `appCtx(ctx)`.
- Config resolution: CLI flags > env vars > `praxis.toml` > `~/.config/praxis/config.toml` > built-in defaults.
- `cmd/watchdog_flags.go` defines shared `watchdogFlags()` and `overlayWatchdogFlags()` used by both `watchdog` and `tui` commands.
- `cluster.Node` has `*bot.Manager` but NOT `*bot.RunnerPool`. Bot lifecycle (start/stop/kill) flows through state files — cluster handlers set status, the `monitorBotStates` goroutine (in `cmd/watchdog.go`) detects changes and calls pool methods.
- Bots are embedded scriptling scripts. The runner (`bot.Runner`) creates a scriptling VM, registers libraries (gossip, AI, shell, llm), and runs `botcore.py` in a tick loop.
- `lib/botcore.py` is a template — `{{VAR}}` placeholders are replaced at spawn time with bot-specific config.

## Testing

- Standard `testing` package + `testutil` helpers. No external test frameworks.
- Cluster handler tests use `testNode()` (creates `Node` without starting gossip) and `testPacket()` (msgpack-encoded `gossip.Packet`).
- Bot tests use `TempProject()` and `TempBot()` to create filesystem fixtures under `t.TempDir()`.
- Sandbox tests use `MockSandbox` from `testutil`.
- No integration tests in CI currently (`internal/integration/` does not exist).

## Gossip Codec

The scriptling gossip library hardcodes `codec.NewVmihailencoMsgpackCodec()` at `extlibs/net/gossip/library.go:1194`. All cluster nodes must use msgpack. Do NOT switch to JSON codec — it will break bot-to-watchdog communication.

## Bot Template System

`lib/botcore.py` uses `{{CONFIG}}`, `{{BOT_ID}}`, etc. as template placeholders. The `bot.Manager.TemplateBytes` field holds the raw template. At spawn, `Manager.Create()` replaces placeholders and writes the result to the bot's directory.

When editing `botcore.py`: changes affect all newly spawned bots. Existing bots keep their copy. Use `/refresh <bot>` to update an existing bot's `bot.py` from the current template, then `/restart` to apply. The file is embedded at build time via `embed.go`.

## Thinking Mode

Each model's thinking/reasoning behavior is configured in `praxis.toml` `[[models.catalog]]` via the `thinking_template` field. The bot looks up the model's entry in `CONFIG["models"]` at runtime.

- `"thinking_template": "glm"` — reference a built-in provider template (qwen, ollama, ollama_compat, openai, anthropic, glm, gemini_flash, mistral)
- `"thinking": {...}` — inline config, overrides template if both present
- Neither field — no thinking control, prompt sent as-is

Built-in templates are defined in `_THINKING_TEMPLATES` dict in `botcore.py`. Resolution: inline `thinking` > `thinking_template` > none.

- `"mode": "prefix"` — prepends a text prefix (e.g., `/no_think`) to the prompt to disable thinking
- `"mode": "json_body"` — passes extra JSON body params (e.g., `{"thinking": {"type": "disabled"}}`) to the API call via `extra_body`

The `_apply_thinking(model_id, prompt, enabled)` helper in `botcore.py` handles all cases. It is used in `_query_model`, the main tick loop, and `_consensus_llm_call`. The per-bot `BotConfig.Thinking` flag (set via `--no-thinking` on spawn) determines whether thinking is on or off; the model's config determines *how* it's applied.

## Plan-Driven Tick Loop

Bots work in a plan-driven loop: (1) handle messages first, (2) execute active plan, (3) assess and create new plan or terminate. Plans live in `entities/plan.md` as Markdown checklists (`[ ]` / `[x]`). The `_plan_state()` helper detects plan state (`"none"`, `"active"`, `"done"`).

### Stuck Detection

If the plan hasn't changed in `BOT_STUCK_TICKS` ticks (default 5), a warning is injected into the tick instructions prompting the bot to try a different approach. The `_check_stuck()` helper tracks a hash of the plan content in bot state.

### Scheduled Actions

The `schedule_action` tool stores reminders in `_reminders` in bot state, keyed by target tick number. Due reminders appear in a `## Reminders` section in the tick message before instructions. Expired reminders are cleaned up automatically.

## Web Tools

- `web_search` — queries DuckDuckGo Lite, parses `result-link`, `result-snippet`, `link-text` classes. Redirect URLs are decoded via `_ddg_extract_url`. HTML tags stripped from results via `_strip_html`.
- `web_fetch` — fetches a URL, strips HTML tags/scripts/styles, returns plain text. Accepts any response < 400 (follows redirects). Respects `BOT_HTTP_ALLOWLIST`.

## TUI: Start with Config Override

`/start <bot> [key=value ...] [message]` — starts a bot with optional config changes and an initial message. Config keys: `model`, `thinking`, `goal`, `scope`, `refresh` (`true` = overwrite `bot.py` from current template). If bot is already running, config is updated but `/restart` is needed to apply.

## TUI: Restart with Config Override

`/restart <bot> [key=value ...] [message]` — kill and restart with the same config override syntax as `/start`. Supports `refresh=true` to update template before restarting.

## TUI: Refresh Template

`/refresh <bot>` — overwrites the bot's `bot.py` with the current `botcore.py` template via `Manager.RefreshTemplate()`. Requires `/restart` to run the updated code.

`/refresh-all` — refreshes all bots' `bot.py` in one command.

## TUI: Tab Completion

Commands that take a bot name (`/start`, `/stop`, `/kill`, `/restart`, `/refresh`) support Tab completion — bot names are auto-populated from the current bot list and updated when the bot panel refreshes.

## Conventions

- No comments in Go code unless explicitly requested.
- Message types are dispatched by `type` string field in `internal/cluster/cluster.go:handleBotMsg`. New message types need: constant in `messages.go`, struct pair (Request/Reply), handler method, case in dispatcher.
- CLI flags use `--kebab-case`. Env vars use `UPPER_SNAKE_CASE`.
- Gossip metadata keys: `role` (watchdog/bot), `id` (bot name or "operator"), `node_name` (watchdog node name).

## Tailscale (tsnet)

Optional remote swarm connectivity via `tailscale.com/tsnet`. Enabled when `--tsnet-hostname` is set. See `docs/networking.md` for full details.

- `internal/cluster/tsnet.go` — `dualListener` (accepts on both local TCP + tsnet), `tsnetDialer` (routes to tsnet for CGNAT/100.x.x.x addresses, fallback to regular TCP for LAN)
- `gossip.Config.DialFunc` / `gossip.Config.ListenFunc` (v0.12.5+) inject tsnet's `Dial`/`Listen`
- Local bots connect via LAN as usual — no tsnet dependency on the bot side
- All isolation (secrets, scope, auth) works identically over both transports

## Lifecycle Hooks

User-defined shell commands or HTTP endpoints that execute at specific bot lifecycle points. Configured in `praxis.toml` under `[hooks]`.

### Hook Events

**Watchdog-side (Go):**

| Event | When | Can block |
|---|---|---|
| `pre_spawn` | Before a bot is created | Yes |
| `post_spawn` | After a bot is created | No |
| `pre_start` | Before a bot runner starts | No |
| `post_start` | After a bot enters running state | No |
| `pre_stop` | Before graceful stop | No |
| `post_stop` | After bot stops cleanly | No |
| `pre_kill` | Before forced kill | No |
| `post_kill` | After bot is killed | No |
| `post_crash` | After bot crashes (includes error/crash count) | No |

**Bot-side (botcore.py):**

| Event | When | Can block |
|---|---|---|
| `pre_tick` | Before LLM call in tick loop | Yes |
| `post_tick` | After successful tick | No |
| `pre_tool_use` | Before a tool executes | Yes |
| `post_tool_use` | After a tool executes | No |
| `on_message` | When a gossip message arrives | No |
| `on_stuck` | When stuck detection triggers | No |

### Configuration

Hooks are defined in `praxis.toml` as arrays of handler objects:

```toml
[hooks]
pre_spawn = [
  { type = "command", command = "/path/to/validate.sh", timeout = 30 }
]
post_tick = [
  { type = "http", url = "http://localhost:8080/hooks/tick", timeout = 10 }
]
pre_tool_use = [
  { type = "command", command = "/path/to/check.sh", timeout = 15 }
]
on_message = [
  { type = "http", url = "http://localhost:8080/hooks/message", async = true }
]
```

Each handler has: `type` (required: "command" or "http"), `command` or `url`, `timeout` (seconds), `async` (boolean).

### Hook Input

Handlers receive JSON with: `hook_event_name`, `bot_id`, `payload` (event-specific). Command hooks receive it on stdin. HTTP hooks receive it as POST body.

### Hook Output

- Exit 0: success. If stdout is valid JSON `{"block": true, "reason": "..."}`, the action is blocked.
- Exit 2: blocking error. stderr text is used as the block reason.
- Other exit codes: non-blocking error, execution continues.

### Architecture

- `internal/hooks/hooks.go` — `Fire()` dispatches to command or HTTP runners, `runCommand()` / `runHTTP()`
- `internal/config/config.go` — `HooksConfig`, `HookHandler` structs, `HooksForEvent()`, `BotHooksAsDict()`
- Bot-side hooks in `botcore.py` — `_run_hook()`, `_run_hook_command()`, `_run_hook_http()`
- Watchdog hooks fire in `runner.go` (start/stop/kill/crash), `spawn.go`, `cmd/spawn.go`, `dashboard.go`
- Bot hooks are injected via `CONFIG["hooks"]` and run inside the scriptling VM

## MCP (Model Context Protocol)

Bots can use tools from MCP servers configured in `praxis.toml` under `[[mcp]]`. Tools are discovered at startup and bridged into the bot's `ToolRegistry` as regular tools (with namespace prefix).

### Configuration

```toml
[[mcp]]
name = "github"
url = "http://localhost:8080/mcp"
namespace = "gh"
bearer_token = ""
```

### Architecture

- `internal/config/config.go` — `MCPServerEntry` struct, `MCPServersAsDict()` method
- `internal/bot/runner.go` — registers `extmcp` library, injects `CONFIG["mcp_servers"]`
- `lib/botcore.py` — `_mcp_clients` list, bridge code before agent creation: creates `mcp.Client`, enumerates tools via `client.tools()`, registers each as `tools.add(name, desc, params, _wrap_tool(name, handler))`
- Tool names are prefixed with namespace: `<namespace>__<tool_name>` (e.g. `gh__search_code`)
- MCP tools go through the same `_wrap_tool` wrapper, so hooks (`pre_tool_use`, `post_tool_use`) fire on them
- If a server is unreachable at startup, the error is logged but the bot still starts without those tools
