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
| `internal/sandbox/` | Shell command sandboxing (bwrap or none). Interface in `sandbox.go`. |
| `internal/tui/` | Terminal UI dashboard (`dashboard.go`). All `/` commands are methods on `Dashboard`. |
| `internal/testutil/` | `MockSandbox`, `TempProject()`, `TempBot()` — use these in tests. |
| `lib/` | Python files embedded into bots at spawn. `botcore.py` is the bot runtime (tools, LLM loop, gossip). |

**Key relationships**:
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

Each model's thinking/reasoning behavior is configured in `models.json` via the `thinking` or `thinking_template` field. The bot looks up the model's entry in `CONFIG["models"]` at runtime.

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

`/start <bot> [key=value ...] [message]` — starts a bot with optional config changes and an initial message. Config keys: `model`, `thinking`, `goal`, `scope`. If bot is already running, config is updated but `/restart` is needed to apply.

`Manager.UpdateConfig()` in `manager.go` writes the fields to `config.json`. The runner reads config fresh on start via `SetVar("CONFIG", ...)`.

## TUI: Refresh Template

`/refresh <bot>` — overwrites the bot's `bot.py` with the current `botcore.py` template via `Manager.RefreshTemplate()`. Requires `/restart` to run the updated code.

## Conventions

- No comments in Go code unless explicitly requested.
- Message types are dispatched by `type` string field in `internal/cluster/cluster.go:handleBotMsg`. New message types need: constant in `messages.go`, struct pair (Request/Reply), handler method, case in dispatcher.
- CLI flags use `--kebab-case`. Env vars use `UPPER_SNAKE_CASE`.
- Gossip metadata keys: `role` (watchdog/bot), `id` (bot name or "operator"), `node_name` (watchdog node name).
