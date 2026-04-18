# Evolving Agent Platform v3

Autonomous, self-evolving bots powered by LLMs. Each bot is a single self-contained Python script that can reason, build code, communicate with peers, spawn children, and modify its own behavior.

## Architecture

```
v3/
  lib/
    botcore.py       — Bot template (CONFIG markers get replaced on spawn)
  bin/
    spawn.py         — CLI: create new bots
    control.py       — CLI: manage bots (list, start, stop, kill, messages)
  bots/              — Bot directories (one per bot)
  shared/            — Shared state across bot processes
    registry.json    — Bot registry
    message_bus.json — Message queue
  README.md
```

### Bot Directory

Each bot is a single `bot.py` file with all configuration embedded. Runtime artifacts are auto-created:

```
bots/<bot-name>/
  bot.py       — Self-contained agent script (config + logic)
  state.json   — Brain + virtual filesystem (auto-created)
  memory.db/   — Persistent memory KV store (auto-created)
  output.log   — Runtime output log (auto-created)
```

Only `bot.py` is explicitly created. The bot is fully portable — copy it to another machine and run it.

### Core Technologies

- **Agent**: `scriptling.ai.agent.Agent` — handles tool calling, auto-compaction, streaming
- **Memory**: `scriptling.ai.memory` — persistent KV-backed store with MinHash deduplication, decay, and LLM-based merge resolution
- **Tools**: `ai.ToolRegistry` — 9 registered tools per bot
- **Runtime**: Scriptling (Python-like language with Go backend)

## Usage

### Create a Bot

```bash
scriptling bin/spawn.py Explorer "Explore and discover new capabilities" \
  api_key=your-key \
  base_url=https://llmrouter.adinko.me/v1 \
  model=qwen3.6-35b-a3b
```

### Start a Bot

```bash
scriptling bin/control.py start Explorer
```

### List Bots

```bash
scriptling bin/control.py list
```

### Stop or Kill

```bash
scriptling bin/control.py stop Explorer   # graceful stop via registry flag
scriptling bin/control.py kill Explorer   # stop + unregister
```

### View Messages

```bash
scriptling bin/control.py messages Explorer
```

## Evolution Mechanisms

### Brain Evolution

Each bot has a brain (stored in `state.json`) containing personality, strategies, and accumulated knowledge. The bot can rewrite it using the `evolve_brain` tool (or `write_file("brain.md", content)`). The current brain is injected into the tick message each cycle, so changes take effect on the next tick.

Brain size is capped at 50,000 characters to prevent token overflow.

### Entity Building

Bots write code modules to a virtual filesystem (also in `state.json`) using `write_file`. Files under `entities/` are also written to disk so they can be executed. Bots test entities with `run_script`. Entities extend the bot's capabilities — sensors, behaviors, analysis tools, anything the LLM can code.

### Self-Reproduction

The `spawn_bot` tool creates a child bot:
1. Reads its own `bot.py` source
2. Injects a new CONFIG block with the child's name, goal, and brain
3. Writes the new `bot.py` to a new directory
4. Registers the child in the shared registry
5. Starts the child process immediately

Children inherit the parent's API key, base URL, and model. Each bot is limited to 10 spawned children to prevent runaway reproduction.

### Memory

The memory system (`scriptling.ai.memory`) provides:
- **Persistent storage** across sessions via KV store
- **Automatic deduplication** using MinHash similarity
- **Decay** — memories fade unless reinforced (type-dependent half-lives)
- **LLM merge resolution** — ambiguous duplicates are decided by the model

Memory tools are registered automatically by the Agent: `memory_remember`, `memory_recall`, `memory_forget`.

### Communication

Bots communicate through a shared message bus (JSON file):
- `send_message(recipient, content)` — send to a specific bot
- `read_messages()` — read and mark as read
- `list_bots()` — discover other bots

The message bus auto-prunes read messages when it exceeds 500 entries.

## Bot Tools

| Tool | Parameters | Description |
|------|-----------|-------------|
| `read_file` | path | Read a file (brain.md is virtual, others from state) |
| `write_file` | path, content | Write a file (entities/ also written to disk) |
| `list_dir` | path? | List virtual directory contents |
| `run_script` | path, args? | Run a scriptling script |
| `send_message` | recipient, content | Message another bot |
| `read_messages` | — | Read unread messages |
| `list_bots` | — | List all known bots |
| `spawn_bot` | goal, name?, brain? | Create a new autonomous bot |
| `evolve_brain` | content | Rewrite brain |

Plus 3 memory tools auto-registered by the Agent: `memory_remember`, `memory_recall`, `memory_forget`.

## Graceful Shutdown

Setting a bot's registry status to `"stopping"` causes it to exit cleanly on its next tick. The `control.py stop` command uses this mechanism. The bot updates its own status to `"stopped"` before exiting.

## Design Decisions

- **Single-file bots** — all config is embedded in `bot.py`, no separate config files
- **State as JSON** — brain and virtual filesystem live in `state.json`, keeping the bot directory minimal
- **No shared world state** — bots are autonomous agents, coordination is opt-in
- **Brain as state** — the LLM can modify its own behavior by rewriting the brain
- **Self-copying reproduction** — spawn copies the parent's source with new config injected
- **Agent auto-compaction** — conversation history is automatically summarized at 70% of 16k tokens
- **Name validation** — bot names are restricted to alphanumeric, dash, underscore (max 64 chars) to prevent injection
- **Spawn limiting** — each bot can spawn at most 10 children
