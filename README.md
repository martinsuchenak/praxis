# Praxis

Autonomous, self-evolving bots powered by LLMs. Each bot is a single self-contained script that can reason, build capabilities, communicate with peers, spawn children, modify its own behavior, and migrate to other machines to seed new swarms.

> Built on [Scriptling](https://github.com/paularlott/scriptling) — a Python-like scripting language, created by [paularlott](https://github.com/paularlott).

## Architecture

```
praxis/
  lib/
    botcore.py        — Bot template (injected on spawn)
    defaults.py       — Runtime constants (injected into each bot)
    prompt.py         — System prompt builder (injected into each bot)
  bin/
    control.py        — All operator commands: spawn, start, stop, list, logs, export, ...
  bots/               — One directory per bot (runtime-created)
  models.example.json — Example model catalog (copy to models.json to use)
  .env.example        — Example env config (copy to .env and fill in)
  README.md
```

No shared directory. Bots coordinate entirely through the gossip cluster.

### Bot Directory

```
bots/<name>/
  bot.py              — Self-contained agent (config + full runtime, injected at spawn)
  brain.md            — Current brain (system prompt addendum, updated by evolve_brain)
  brain_history.json  — Last 5 brain snapshots
  state.json          — Operational state: fitness counters, gossip port, last activity
  status.json         — Live status written each tick (read by control.py)
  memory.db/          — Persistent KV memory store
  bot.log             — Structured log: ticks, tool calls, messages, errors (rolling 500 KB)
  output.log          — stdout/stderr from nohup (fallback)
  entities/           — All files the bot writes (plans, knowledge, scripts, data, ...)
```

### Core Technologies

- **Agent**: `scriptling.ai.agent.Agent` — tool calling, auto-compaction, streaming
- **Memory**: `scriptling.ai.memory` — KV-backed, MinHash dedup, decay, LLM merge
- **Cluster**: `scriptling.net.gossip` — membership, metadata sync, direct messaging, request/reply, node groups, leader election
- **Discovery**: `scriptling.net.multicast` — subnet bootstrap, periodic announce
- **Search**: `scriptling.grep` / `scriptling.sed` — in-process file search and replace (no subprocess)
- **Runtime**: Scriptling (Python-like language with Go backend, subprocess disabled for bots)

## Usage

### Configuration

Create a `.env` file in the project root by copying `.env.example` (or export environment variables):

```bash
BOT_API_KEY=your-key
BOT_BASE_URL=your-openai-compatible-endpoint
BOT_MODEL=your-model-name
BOT_TICK_INTERVAL=30          # optional: seconds between ticks (default: 30)
BOT_SCRIPT_TIMEOUT=30         # optional: scriptling script execution timeout (default: 30)
BOT_LOG_VERBOSE=true          # optional: disable log truncation for debugging
BOT_GOSSIP_SECRET=shared-key  # optional: authenticate inter-bot gossip messages
BOT_STALE_THRESHOLD=120       # optional: seconds before a "running" bot is flagged STALE (default: 120)
BOT_MAX_BACKOFF=600           # optional: max backoff seconds after repeated tick errors (default: 600)
BOT_MAX_CONCURRENT=1          # optional: max concurrent LLM calls per model (default: 1, override per-model in models.json)
BOT_TICK_MAX_ITERATIONS=5     # optional: max tool-call iterations per tick (default: 5)
BOT_HTTP_ALLOWLIST=           # optional: comma-separated domains bots may call via http_request (default: unrestricted)
BOT_SHELL_ALLOWLIST=          # optional: comma-separated executables bots may run via shell (default: unrestricted except curl/wget)
BOT_SHELL_SANDBOX=true        # optional: sandbox shell commands with bwrap (default: true, requires bwrap installed)
BOT_SHELL_MOUNTS=             # optional: extra mounts inside the sandbox, format: mode:host_path:container_path (comma-separated)
                              #   e.g. BOT_SHELL_MOUNTS=ro:/shared/data:/data,rw:/tmp/scratch:/scratch
```

API keys are never baked into bot source files — bots read them from the environment at runtime.

### Workspaces (optional)

Create a `workspaces.json` file (see `workspaces.example.json`) to give bots access to external project directories:

```json
{
  "myapp": {
    "path": "/home/user/projects/myapp",
    "gossip_secret": "myapp-workspace-secret",
    "default_scope": "isolated"
  },
  "website": {
    "path": "/home/user/projects/website",
    "default_scope": "isolated"
  }
}
```

Each workspace can optionally define:
- `gossip_secret` — a secret used for message authentication by bots in this workspace. Bots with different secrets drop each other's messages. If omitted, bots use the global `BOT_GOSSIP_SECRET`.
- `default_scope` — the communication scope for bots spawned into this workspace (default: `isolated`). Can be overridden per-bot at spawn time.

Spawn a bot with a workspace and its `read_file`/`search`/`shell` tools can access the project directly:

```bash
scriptling bin/control.py spawn DevBot "Refactor auth" workspace=myapp

# Gateway bot that can talk to two workspaces:
scriptling bin/control.py spawn Coordinator "Coordinate frontend and backend" \
  workspace=myapp scope=gateway allowed_workspaces=website
```

The workspace name is resolved from `workspaces.json` to a host path by the operator at spawn time. The path is stored in the bot's `status.json` (never in its source code or CONFIG). Bots read it at startup and can access workspace files via their own tools (`read_file`, `write_file`, `shell`, etc.) — the path is added to scriptling's `--allowed-paths` and the bwrap sandbox mounts it at the real path.

Children automatically inherit the workspace: when a bot spawns a child, the watchdog detects the parent-child relationship and copies the workspace path to the child. Bots cannot change their workspace or spawn into a different one.

Without a workspace, bots are fully isolated in their own directory with no external mounts.

### Model Catalog (optional)

Create a `models.json` file (see `models.example.json` for the format) to give bots awareness of other models they can use:

```json
[
  {
    "id": "qwen/qwen3.6-35b-a3b",
    "label": "Qwen 3.6 35B",
    "description": "Small, fast model. Good for quick fixes, summaries, formatting.",
    "cost": "low",
    "strengths": ["fast", "simple tasks", "formatting", "summaries"]
  },
  {
    "id": "qwen/qwen3-235b-a22b",
    "label": "Qwen 3 235B",
    "description": "Large reasoning model for complex analysis and multi-step planning.",
    "cost": "high",
    "strengths": ["reasoning", "complex analysis", "planning", "architecture"]
  }
]
```

Each entry has: `id` (model name matching your API), `label` (human-readable), `description` (what it's good for), `cost` (low/medium/high), `strengths` (tag list), and optionally `concurrency` (max simultaneous LLM calls for this model — overrides `BOT_MAX_CONCURRENT`).

When present, bots get an **Available Models** section in their system prompt and a `list_models` tool. They can then use `query_model` for one-shot calls to a specific model or pass `model=` when spawning children. The model catalog is baked into each bot's source so child and migrated bots carry it forward.

If `models.json` is absent, bots only know their own model and `list_models` reports no additional models available.

### Create a Bot

```bash
scriptling bin/control.py spawn Explorer "Explore and discover new capabilities"
```

With options:

```bash
scriptling bin/control.py spawn Scout "Scout the environment" \
  seeds=192.168.1.10:37291 model=qwen/qwen3-235b-a22b


scriptling bin/control.py spawn Worker "Process data quickly" thinking=false
```

Available options: `model=`, `brain=`, `seeds=`, `thinking=false`, `workspace=`, `scope=open|isolated|gateway|family`, `allowed_workspaces=ws1,ws2`

Then start it:

```bash
scriptling bin/control.py start Explorer
```

### Start / Stop / Kill / Restart

```bash
scriptling bin/control.py start Explorer     # launch process
scriptling bin/control.py stop Explorer      # graceful stop (next tick)
scriptling bin/control.py kill Explorer      # immediate SIGTERM
scriptling bin/control.py restart Explorer   # kill + start
scriptling bin/control.py remove Explorer    # kill + delete bot directory
```

Bulk operations:

```bash
scriptling bin/control.py start-all          # start all stopped bots
scriptling bin/control.py stop-all           # graceful stop all running
scriptling bin/control.py kill-all           # SIGTERM all bots
scriptling bin/control.py restart-stale      # restart all bots flagged STALE
```

### List / Status

```bash
scriptling bin/control.py list               # local status files (flags STALE bots)
scriptling bin/control.py status             # live swarm view via gossip with fitness counters
```

### View Logs

```bash
scriptling bin/control.py logs Explorer         # last 40 lines of activity + errors + output
scriptling bin/control.py logs Explorer 100     # last 100 lines
scriptling bin/control.py tail Explorer         # follow bot.log in real time
scriptling bin/control.py tail Explorer output  # follow output.log in real time
```

### Send a Message

Send a message or task to a running bot:

```bash
scriptling bin/control.py send Explorer "Focus on scraping news sites next"
```

The message lands in the bot's inbox as `from: operator` and is picked up on the next tick.

### Export a Bot

Package a bot and all operator tools into a portable archive for transfer to another machine:

```bash
scriptling bin/control.py export Explorer
```

This creates `Explorer-<timestamp>.tar.gz` in the project root containing:

```
Explorer-<timestamp>/
  bots/Explorer/     — bot state + entities (no logs or memory.db)
  bin/control.py     — full operator CLI on the target machine
  lib/               — bot template and defaults
  .env.example       — credentials template
  bootstrap.sh       — generated launcher
```

Transfer and run:

```bash
tar xzf Explorer-<timestamp>.tar.gz
cd Explorer-<timestamp>
cp .env.example .env   # fill in BOT_API_KEY, BOT_BASE_URL, BOT_MODEL
bash bootstrap.sh
```

The bot starts, uses its embedded `seed_addrs` to rejoin the original swarm (if reachable), or seeds a new swarm on the local subnet. `bin/control.py` is available for full management on the target machine.

### Watchdog + Command Proxy

Auto-restart crashed bots and proxy shell commands (bots have no subprocess access):

```bash
scriptling bin/control.py watchdog           # runs until Ctrl+C
```

The watchdog joins the gossip cluster as `role=watchdog`. Bots send `shell_req` gossip requests to the watchdog, which enforces an allowlist and wraps commands in a `bwrap` sandbox. If `bwrap` is not installed, commands run unsandboxed (with a log warning).

When a bot has a workspace configured, the watchdog adds the workspace path to `--allowed-paths` when starting it, and mounts it at the real path inside the bwrap sandbox. Bots access workspace files using their own tools (`read_file`, `write_file`, `search`) and `shell` commands with the same path.

## Networking

### Discovery

On startup each bot:

1. Tries any `seed_addrs` from its CONFIG to join an existing gossip cluster.
2. Falls back to multicast on `239.255.13.37:19373` — sends a discover, waits 3s for an announce from a peer.
3. If nothing responds, starts as the root node of a new swarm.
4. Broadcasts a multicast announce so future bots can find it.

Every 10 ticks (≈5 min) each bot re-announces on multicast, so bots that start later can join.

### Gossip Cluster

Once a bot joins a gossip cluster, membership and metadata propagate automatically across all machines. Each bot publishes its ID, goal, and gossip address as metadata. `list_bots()` returns the live cluster view — no shared file needed.

### Messaging

All inter-bot messages are sent directly via `gossip.send_to()`. Request/response patterns use `gossip.send_request()` / `handle_with_reply()`. Message types in the payload:

| `type` | Direction | Purpose |
|---|---|---|
| `message` | one-way | Direct message, delivered to recipient's inbox |
| `brain_req` | request/reply | Request another bot's brain (for crossover) — reply contains `{"brain": "..."}` |
| `consensus_req` | request/reply | Ask a peer to answer a question — reply contains `{"answer": "...", "from": "..."}` |
| `task_complete` | one-way | Child bot reporting task completion to parent |
| `stop` | one-way | Remote graceful stop signal |
| `shell_req` | request/reply | Bot -> watchdog command proxy — reply contains `{"exit_code": ..., "stdout": ..., "stderr": ...}` |
| `relay_req` | request/reply | Bot -> watchdog cross-workspace relay — reply contains `{"status": "relayed", ...}` or `{"error": ...}` |
| `relayed_message` | one-way | Watchdog -> bot relayed cross-workspace message — contains `from`, `content` |

### Communication Scope

Bots have a **scope** that controls which peers they can see and message. Scope is set at spawn time (workspace default + optional per-bot override). The four modes:

| Scope | Visibility | Cross-workspace |
|---|---|---|
| `open` | All bots on the gossip network | Direct messaging |
| `isolated` | Same-workspace bots only | None |
| `gateway` | Same-workspace + allowed workspaces | Via watchdog relay |
| `family` | Parent and children only | None |

**How it works:**

- Each bot publishes `scope` and `workspace` as gossip metadata.
- `list_bots` and `send_message` filter peers by scope rules.
- Gateway bots can send messages to bots in `allowed_workspaces` — the watchdog relays the message on their behalf. The target receives it as a `relayed_message`.
- Incoming consensus requests and relayed messages always reach a bot regardless of scope.
- Per-workspace `gossip_secret` provides application-level message filtering — bots drop messages with the wrong secret.
- Scope is stored in `status.json` (read once at startup into a module variable). Bots cannot change their scope at runtime.

### Node Groups and Leader Election

Bots register with `role=bot` metadata and join a criteria-based node group (`{"role": "bot"}`). Tools like `list_bots` and `send_message` iterate over this group rather than all cluster nodes (which includes the watchdog).

A leader election runs automatically with 51% quorum among bot-role nodes. The leader status is surfaced in tick messages (`"You are the swarm leader."`) and in `control list` / `control status`. Leader-specific behaviour can be added to bot brains.

## Evolution Mechanisms

### Brain Evolution

The brain is in the agent's **system prompt** (not the tick message). Changes via `evolve_brain` take effect on the very next tick. The last 5 brain snapshots are kept in `state.json` and shown in the system prompt so the bot can see its own history.

### Fitness Tracking

Each bot tracks fitness counters in `state.json` and `status.json`:

| Counter | Meaning |
|---|---|
| `ticks_alive` | Number of ticks completed |
| `spawns` | Children created |
| `messages_sent` | Messages sent to peers |
| `brain_evolutions` | Times the brain was rewritten |
| `consensus_asked` | Times the bot asked peers for consensus |
| `consensus_answered` | Times the bot answered a peer's consensus request |
| `tasks_completed` | Times the bot reported task completion to a parent |

Fitness is visible in the tick message so the LLM can reason about its own progress.

### Self-Replication (`spawn_bot`)

1. Reads own `bot.py` source.
2. Injects child config (including the parent's current gossip address as `seed_addrs`).
3. Writes child `bot.py` and launches it.
4. Child immediately joins the parent's gossip cluster and becomes visible to all peers.

Optionally pass `task_id=` when spawning — the child's initial brain will include instructions to call `complete_task` when done.

### Genetic Crossover (`spawn_hybrid`)

1. Sends a `brain_req` to a target bot via gossip.
2. Target bot responds with its current brain.
3. Parent merges both brains and spawns a child with the combined brain.
4. The child inherits strategies from two parents.
5. An optional `model` parameter lets the child run on a different model than either parent.

### Task Delegation

A parent bot can spawn a child with a `task_id`. The child calls `complete_task(parent_bot, task_id, result)` when done, which delivers a `task_complete` message to the parent's inbox. The parent picks it up on the next tick.

### Consensus

A bot can poll `n` peers (any positive odd number) on a question and get the majority answer back:

```
ask_consensus(question="Should I prioritise scraping or analysis?", n=3)
```

Each peer answers independently using its own LLM (without thinking mode, for speed). The result includes all individual responses, the majority answer, and the agreement ratio. The bot and its peers both store the exchange in memory.

### Multi-Model Usage

Every bot knows its own model (shown in each tick message). If a `models.json` catalog is configured, bots also see all available models with descriptions and strengths in their system prompt. Two complementary mechanisms:

**Model as genome** — `spawn_bot` and `spawn_hybrid` accept an optional `model` parameter. Children can run on a different model than their parent. Combined with fitness tracking, this creates natural selection: if bots on one model consistently spawn/communicate/evolve more, the swarm drifts toward that model over generations without any explicit selection logic.

**Per-task model routing** — `query_model(model, prompt, system?, thinking?)` sends a one-shot prompt to any model available on the same `base_url`. Useful for cheap summarisation, specialised generation, or any task where the main model is overkill.

## Bot Tools

| Tool | Parameters | Description |
|---|---|---|
| `read_file` | path | Read a file (`brain.md` reads the brain) |
| `read_file_range` | path, start, end? | Read a line range from a file (1-indexed) |
| `write_file` | path, content, description? | Write a file (`entities/` also written to disk; `brain.md` calls evolve_brain) |
| `append_file` | path, content | Append to a file (creates it if absent) |
| `delete_file` | path | Delete a file |
| `replace_in_file` | path, old, new | Replace text in a file (replaces all occurrences; `brain.md` supported) |
| `list_dir` | path? | List virtual directory |
| `search` | pattern, path?, glob?, ignore_case? | Regex search across files (uses scriptling.grep, no subprocess) |
| `shell` | command, cwd?, timeout? | Run a shell command via the watchdog command proxy (sandboxed with bwrap) |
| `run_script` | path, args? | Run a scriptling script |
| `send_message` | recipient, content | Direct message by bot ID |
| `complete_task` | parent_bot, result, task_id? | Report task completion to parent bot |
| `read_messages` | — | Drain the inbox |
| `list_bots` | — | Live swarm view from gossip |
| `spawn_bot` | goal, name?, brain?, model?, task_id? | Create a child bot |
| `spawn_hybrid` | other_bot, goal, name?, model? | Crossover with another bot's brain |
| `evolve_brain` | content, reason? | Rewrite your brain |
| `query_model` | model, prompt, system?, thinking? | One-shot call to any model for a subtask |
| `list_models` | — | List available models with descriptions and strengths |
| `http_request` | url, method?, body?, content_type?, headers?, timeout? | HTTP request (GET/POST/PUT/DELETE/PATCH) |
| `ask_consensus` | question, n? | Poll n peers and return the majority answer |
| `memory_remember` | content, type?, importance?, reason? | Store a fact or observation in persistent memory |
| `memory_recall` | query?, limit?, type? | Search memories by keyword and similarity |
| `memory_forget` | id | Remove a memory by ID |

Memory tools are manually registered (not via agent auto-registration) so they go through the activity logging wrapper.

## Design Decisions

- **No shared filesystem** — registry and messaging are entirely gossip-based; bots on different machines are peers.
- **Brain in system prompt** — the brain has stable priority over tick context and isn't consumed by compaction.
- **Per-bot status.json** — written each tick; `control.py` reads it without any shared file or locking.
- **Brain and history on disk** — `brain.md` and `brain_history.json` live as plain files alongside the bot, not inside `state.json`. Brain updates don't rewrite the full state. `state.json` only holds fitness counters, gossip port, and last-tick activity summary. Existing bots with old-style state.json are automatically migrated on first startup.
- **pkill-based kill** — `control kill` uses `pkill -f` with the bot's full absolute path, avoiding substring collisions.
- **Path traversal guard** — `_safe_path` rejects `..` components and absolute paths to keep bots within their own directory.
- **Bot log** — every tick writes a structured log to `bot.log` (levelled entries: `START`, `INFO`, `MSG`, `TOOL`, `OK`/`ERR`, `ERROR`) with a rolling 500 KB cap. All tool calls, messages, errors, and tick summaries go here. Use `control logs` or `control tail` to inspect.
- **Error handling** — tick exceptions are logged with level `ERROR` and trigger exponential backoff; the loop never silently swallows failures.
- **Error backoff** — repeated tick failures trigger exponential backoff (up to `BOT_MAX_BACKOFF` seconds) so a broken bot doesn't hammer the API.
- **Atomic writes** — all JSON writes use write-to-`.tmp`-then-rename.
- **Spawn limiting** — each bot can create at most 10 children.
- **Consensus inline** — incoming `consensus_req` gossip messages are answered inline by the `handle_with_reply` handler, not deferred to tick time. This is simpler than the old queue-based approach.
- **Thinking mode** — controlled per-bot via the `thinking` CONFIG field; implemented by prepending `/no_think` to the LLM message rather than a parameter, since that's what the model router requires.
- **Memory tool observability** — memory tools are registered manually (not via `agent.Agent(memory=mem)` auto-registration) so they go through `_wrap_tool` for activity logging. The `reason` parameter on `memory_remember` and `evolve_brain` is optional, since smaller models may fail to provide required parameters.
- **Brain evolution logging** — `evolve_brain` logs the line-count diff (`old -> new lines`) and stores an optional `reason` in brain history for traceability.
- **File awareness** — a flat `.index.md` in `entities/` lists all knowledge files with descriptions, sizes, and ages. Rebuilt on every write/delete/replace and shown in the tick message so the bot sees its knowledge at a glance.
- **Debug dumps** — when `LOG_VERBOSE=true`, each tick writes `debug/tick-N.md` containing the full system prompt and tick message for offline debugging.
- **No credentials in source** — API keys and the base URL are never written into bot.py. Bots read `BOT_API_KEY` and `BOT_BASE_URL` from the environment at runtime.
- **`BOT_IP` env var** — bots cannot detect their own IP (no subprocess), so `control.py` detects the local IP and passes it as `BOT_IP` in the environment. Bots use this for their gossip address.
- **No subprocess in bot runtime** — bots are spawned with `--disable-lib=subprocess` and cannot spawn processes directly. The `shell` tool proxies commands through the watchdog via gossip request/reply. The `search` tool uses `scriptling.grep` (in-process) instead of spawning `rg`/`grep`. File edits can use `replace_in_file` via `scriptling.sed`.
- **Layered filesystem sandboxing** — three complementary layers: (1) `scriptling --allowed-paths` restricts the scriptling `os`/`pathlib`/`glob` libraries to the bot's own dir, `bots/`, and `.locks/` — enforced by the runtime, not bypassable via source edits since env vars are read-only; (2) `subprocess` library is disabled entirely — bots cannot spawn processes; (3) `bwrap` sandboxes shell commands on the watchdog — the bot's directory is mounted as `/`, system dirs (`/usr`, `/bin`, `/lib`, `/etc`) are read-only, `/tmp` is writable. Set `BOT_SHELL_SANDBOX=false` to disable. If `bwrap` is not installed a warning is logged and commands run unsandboxed.
- **Watchdog command proxy** — the watchdog (`control watchdog`) joins the gossip cluster and handles `shell_req` requests from bots. It enforces a shell allowlist (`BOT_SHELL_ALLOWLIST`), blocks `curl`/`wget` (bots should use `http_request` instead), and wraps commands in `bwrap`. Workspace mounts are resolved per-request from `workspaces.json` using the bot's `status.json`.
- **Workspaces** — bots working on external projects get a named workspace. The operator provides a name at spawn time; `control.py` resolves it from `workspaces.json` and stores the host path in `status.json` (not in bot source). The path is added to `--allowed-paths` and mounted in bwrap. Bots can read/write workspace files with their own tools. Children inherit the workspace via the watchdog (parent-child tracking). Bots cannot change their workspace or spawn into a different one.
- **Gossip request/reply** — brain requests and consensus use `gossip.send_request()` / `handle_with_reply()` instead of manual rendezvous queues. The handler runs synchronously in the gossip goroutine — consensus answers are computed inline (blocking the goroutine briefly) but avoid the complexity of deferred queues.
- **Node groups and leader election** — bots join a `{"role": "bot"}` criteria group so tools iterate only over bot peers (not the watchdog). A leader election with 51% quorum provides a swarm coordinator. Leader status is visible in tick messages and `control list`.
- **Gossip auth** — optional shared secret via `BOT_GOSSIP_SECRET`. When set, all inter-bot messages include `_secret` in the payload and unauthenticated messages are dropped. Bots on different machines just need the same secret in their `.env`.
- **Stale detection** — `control list` flags bots as STALE if `last_tick_ts` (updated every tick) is older than `BOT_STALE_THRESHOLD` seconds (default 120).
- **Crash recovery** — `control watchdog` periodically checks if running bots have a live process and auto-restarts any that crashed.
- **Export bundles tools** — `control export` packages the bot together with `bin/control.py` and `lib/` so the operator has full management capability on the target machine without a separate install.
- **Single operator script** — all operator actions (spawn, start, stop, export, ...) live in `bin/control.py`; there is no separate spawn script.
- **`search` tool** — regex search across `entities/` using `scriptling.grep` (in-process, no subprocess). Returns `file:line:match` format so the bot can `read_file_range` only the relevant section.
- **`replace_in_file` tool** — uses `scriptling.sed.replace()` for efficient in-place text replacement without reading/writing entire files. Supports single or global replacement. Works on `brain.md` too (delegates to `evolve_brain`).
- **`shell` tool** — bots proxy shell commands to the watchdog via gossip request/reply. The watchdog enforces an allowlist, blocks `curl`/`wget`, and wraps commands in `bwrap`. Bots cannot run shell commands without a running watchdog.
- **`http_request`** — single HTTP tool covering GET, POST, PUT, DELETE, PATCH with optional body, `Content-Type`, and extra headers. Returns `http_status` (real HTTP code, not curl exit code).
- **Per-model concurrency cap** — before each tick's LLM call, bots acquire a slot under `.locks/<model>/`. Each bot writes a timestamped file; slots held longer than the request timeout are automatically treated as stale (handles crashes). `concurrency` in `models.json` sets the limit per model; `BOT_MAX_CONCURRENT` is the fallback. This prevents slow models from being hammered by concurrent requests that all time out.
- **Tick iteration cap** — `BOT_TICK_MAX_ITERATIONS` limits the number of tool-call rounds per tick (default 5). Useful for slow models where shorter sessions reduce queuing pressure.
- **Communication scope** — bots have a scope (`open`, `isolated`, `gateway`, `family`) that controls peer visibility and messaging. Set at spawn time from the workspace default with optional per-bot override. Enforced at two layers: (1) per-workspace `gossip_secret` for application-level message filtering, (2) tool-level filtering in `list_bots` and `send_message`. Gateway bots relay cross-workspace messages through the watchdog, which validates `allowed_workspaces` before forwarding. Scope is read from `status.json` into a module variable at startup — bots cannot change it at runtime. Parent-child communication (`complete_task`) is always allowed regardless of scope.
