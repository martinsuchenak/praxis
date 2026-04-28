# Architecture

## File Layout

```
praxis/
  lib/
    botcore.py          Bot template — embedded into the praxis binary at build time
  cmd/                  CLI command implementations
  internal/
    bot/                Bot manager: spawn, start, stop, state, runner pool
    cluster/            Gossip node, message dispatcher, shell/spawn/relay handlers
    sandbox/            bwrap sandbox wrapper
    tui/                Terminal UI dashboard
  bots/                 One directory per bot (runtime-created, git-ignored)
  .locks/               Per-model concurrency slots (runtime-created, git-ignored)
  main.go
  embed.go              Embeds lib/botcore.py into the binary
  go.mod
  workspaces.json       Workspace definitions (git-ignored)
  models.json           Model catalog (git-ignored)
  .env                  Environment config (git-ignored)
```

### Bot Directory

```
bots/<name>/
  bot.py                Self-contained agent script (injected at spawn)
  brain.md              Hot brain layer — system prompt addendum
  brain_history.json    Last 5 brain snapshots
  memory.md             Warm brain layer — on-demand recall
  state.json            Fitness counters, gossip port, last-tick summary
  status.json           Live status written each tick (read by praxis)
  memory.db/            Persistent KV memory store
  bot.log               Structured rolling log (500 KB cap)
  output.log            stdout/stderr from process launch
  entities/             All files the bot creates (knowledge, plans, data, ...)
  entities/.index.md    Auto-maintained index of all entity files
```

## Security Model

### No subprocess in bots

Bots run with `--disable-lib=subprocess`. They cannot spawn processes directly. The `shell` tool proxies commands to the watchdog via gossip request/reply.

### Layered filesystem sandboxing

Three complementary layers:

1. **scriptling `--allowed-paths`** — restricts file access to the bot's own directory, `bots/`, and `.locks/`. Enforced by the runtime, not bypassable via source edits.
2. **subprocess disabled** — bots cannot escape the file sandbox via process launch.
3. **bwrap sandbox** — shell commands run on the watchdog with the bot's directory as `/`, system directories read-only, and `/tmp` writable. Set `BOT_SHELL_SANDBOX=none` to disable.

The workspace directory is an explicit exception: added to `--allowed-paths` and mounted in bwrap at the real host path.

### No credentials in source

API keys and base URLs are never written into `bot.py`. Bots read `BOT_API_KEY` and `BOT_BASE_URL` from the environment at runtime. `BOT_IP` is set by the watchdog process launcher — bots cannot detect their own IP.

### Path traversal guard

`_safe_path` in botcore.py rejects `..` components and absolute paths, keeping bots within their own directory.

## Key Design Decisions

- **No shared filesystem** — registry and messaging are entirely gossip-based; bots on different machines are peers.
- **Brain in system prompt** — the brain has stable priority over tick context and isn't consumed by compaction.
- **Atomic writes** — all JSON writes use write-to-`.tmp`-then-rename.
- **Stale detection** — `praxis list` flags bots as STALE if `last_tick_ts` is older than `BOT_STALE_THRESHOLD` seconds.
- **Error backoff** — repeated tick failures trigger exponential backoff (up to `BOT_MAX_BACKOFF`) so a broken bot doesn't hammer the API.
- **pkill-based kill** — `kill` uses `pkill -f` with the bot's full absolute path, avoiding substring collisions.
- **Tick iteration cap** — `BOT_TICK_MAX_ITERATIONS` limits tool-call rounds per tick. Useful for slow models where shorter sessions reduce queuing pressure.
- **Memory tool observability** — memory tools are registered manually (not via agent auto-registration) so they go through `_wrap_tool` for activity logging.
- **Debug dumps** — when `BOT_LOG_VERBOSE=true`, each tick writes `debug/tick-N.md` with the full system prompt and tick message.
- **`bot.py` is self-contained** — the model catalog, goal, and CONFIG are baked in at spawn. Child and migrated bots carry the full runtime forward.
- **Consensus in background** — the LLM call for `consensus_req` runs in a `runtime.background()` goroutine and shares results back via a named `Queue` (30 s max). The gossip goroutine is never blocked indefinitely.
- **Spawn limiting** — each bot can create at most 10 children.
- **File index** — `entities/.index.md` is rebuilt on every write/delete/replace and shown in the tick message so the bot sees its knowledge at a glance.

## Gossip Wire Protocol

All messages between bots and the watchdog use a single gossip message type (`MSG_USER = 128`). A `type` field in the payload acts as a discriminator — the watchdog dispatches internally to the correct handler.

This means a single gossip `HandleFuncWithReply` registration on the watchdog handles shell requests, spawn requests, and relay requests. The Python side sends all requests on `GOSSIP_MSG = gossip.MSG_USER`.
