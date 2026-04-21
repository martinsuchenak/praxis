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
  output.log          — stdout/stderr from nohup
  errors.log          — Tick errors with timestamps
  activity.log        — Per-tick trace: inputs, tool calls, results (rolling 100 KB)
  entities/           — All files the bot writes (plans, knowledge, scripts, data, ...)
```

### Core Technologies

- **Agent**: `scriptling.ai.agent.Agent` — tool calling, auto-compaction, streaming
- **Memory**: `scriptling.ai.memory` — KV-backed, MinHash dedup, decay, LLM merge
- **Cluster**: `scriptling.net.gossip` — membership, metadata sync, direct messaging
- **Discovery**: `scriptling.net.multicast` — subnet bootstrap, periodic announce
- **Runtime**: Scriptling (Python-like language with Go backend)

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
```

API keys are never baked into bot source files — bots read them from the environment at runtime.

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

Available options: `model=`, `brain=`, `seeds=`, `thinking=false`

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
scriptling bin/control.py tail Explorer         # follow output.log in real time
scriptling bin/control.py tail Explorer activity  # follow activity.log in real time
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

### Watchdog

Auto-restart crashed bots:

```bash
scriptling bin/control.py watchdog           # runs until Ctrl+C
```

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

All inter-bot messages are sent directly via `gossip.send_to()`. Message types in the payload:

| `type` | Purpose |
|---|---|
| `message` | Direct message, delivered to recipient's inbox |
| `brain_req` | Request another bot's brain (for crossover) |
| `brain_resp` | Brain response to a crossover request |
| `consensus_req` | Ask a peer to answer a question |
| `consensus_resp` | Peer's answer to a consensus request |
| `task_complete` | Child bot reporting task completion to parent |
| `stop` | Remote graceful stop signal |

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
| `write_file` | path, content | Write a file (`entities/` also written to disk; `brain.md` calls evolve_brain) |
| `append_file` | path, content | Append to a file (creates it if absent) |
| `delete_file` | path | Delete a file |
| `list_dir` | path? | List virtual directory |
| `search` | pattern, path?, glob?, ignore_case? | Regex search across files (ripgrep, falls back to grep) |
| `shell` | command, cwd?, timeout? | Run any shell command (`git`, `python3`, `npm`, etc.) |
| `run_script` | path, args? | Run a scriptling script |
| `send_message` | recipient, content | Direct message by bot ID |
| `complete_task` | parent_bot, result, task_id? | Report task completion to parent bot |
| `read_messages` | — | Drain the inbox |
| `list_bots` | — | Live swarm view from gossip |
| `spawn_bot` | goal, name?, brain?, model?, task_id? | Create a child bot |
| `spawn_hybrid` | other_bot, goal, name?, model? | Crossover with another bot's brain |
| `evolve_brain` | content | Rewrite your brain |
| `query_model` | model, prompt, system?, thinking? | One-shot call to any model for a subtask |
| `list_models` | — | List available models with descriptions and strengths |
| `http_request` | url, method?, body?, content_type?, headers?, timeout? | HTTP request (GET/POST/PUT/DELETE/PATCH) |
| `ask_consensus` | question, n? | Poll n peers and return the majority answer |

Plus 3 memory tools auto-registered by the Agent: `memory_remember`, `memory_recall`, `memory_forget`.

## Design Decisions

- **No shared filesystem** — registry and messaging are entirely gossip-based; bots on different machines are peers.
- **Brain in system prompt** — the brain has stable priority over tick context and isn't consumed by compaction.
- **Per-bot status.json** — written each tick; `control.py` reads it without any shared file or locking.
- **Brain and history on disk** — `brain.md` and `brain_history.json` live as plain files alongside the bot, not inside `state.json`. Brain updates don't rewrite the full state. `state.json` only holds fitness counters, gossip port, and last-tick activity summary. Existing bots with old-style state.json are automatically migrated on first startup.
- **pkill-based kill** — `control kill` uses `pkill -f` with the bot's full absolute path, avoiding substring collisions.
- **Path traversal guard** — `_safe_path` rejects `..` components and absolute paths to keep bots within their own directory.
- **Activity log** — every tick writes a structured trace to `activity.log` (tool name, args, result summary) with a rolling 100 KB cap; use `control logs` or `control tail` to inspect what a bot is doing.
- **Error log** — tick exceptions are written to `errors.log` with timestamps; the loop never silently swallows failures.
- **Error backoff** — repeated tick failures trigger exponential backoff (up to `BOT_MAX_BACKOFF` seconds) so a broken bot doesn't hammer the API.
- **Atomic writes** — all JSON writes use write-to-`.tmp`-then-rename.
- **Spawn limiting** — each bot can create at most 10 children.
- **Consensus deferred** — incoming `consensus_req` gossip messages are queued and processed at tick time, not inside the gossip callback, to avoid blocking the gossip goroutine.
- **Thinking mode** — controlled per-bot via the `thinking` CONFIG field; implemented by prepending `/no_think` to the LLM message rather than a parameter, since that's what the model router requires.
- **No credentials in source** — API keys and the base URL are never written into bot.py. Bots read `BOT_API_KEY` and `BOT_BASE_URL` from the environment at runtime. This prevents bots from reading their own source and using the endpoint directly via `shell` or `http_request`.
- **Gossip auth** — optional shared secret via `BOT_GOSSIP_SECRET`. When set, all inter-bot messages include `_secret` in the payload and unauthenticated messages are dropped. Bots on different machines just need the same secret in their `.env`.
- **Stale detection** — `control list` flags bots as STALE if `last_tick_ts` (updated every tick) is older than `BOT_STALE_THRESHOLD` seconds (default 120).
- **Crash recovery** — `control watchdog` periodically checks if running bots have a live process and auto-restarts any that crashed.
- **Export bundles tools** — `control export` packages the bot together with `bin/control.py` and `lib/` so the operator has full management capability on the target machine without a separate install.
- **Single operator script** — all operator actions (spawn, start, stop, export, ...) live in `bin/control.py`; there is no separate spawn script.
- **`shell` tool** — bots can run arbitrary shell commands (`git`, `python3`, `npm`, `docker`, etc.) with stdout/stderr capture and a configurable timeout. `cwd` defaults to the bot's own directory.
- **`search` tool** — regex search across `entities/` using ripgrep if available, falling back to `grep -rn`. Returns `file:line:match` format so the bot can `read_file_range` only the relevant section.
- **`http_request`** — single HTTP tool covering GET, POST, PUT, DELETE, PATCH with optional body, `Content-Type`, and extra headers. Returns `http_status` (real HTTP code, not curl exit code).
- **Per-model concurrency cap** — before each tick's LLM call, bots acquire a slot under `.locks/<model>/`. Each bot writes a timestamped file; slots held longer than the request timeout are automatically treated as stale (handles crashes). `concurrency` in `models.json` sets the limit per model; `BOT_MAX_CONCURRENT` is the fallback. This prevents slow models from being hammered by concurrent requests that all time out.
- **Tick iteration cap** — `BOT_TICK_MAX_ITERATIONS` limits the number of tool-call rounds per tick (default 5). Useful for slow models where shorter sessions reduce queuing pressure.
