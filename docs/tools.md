# Bot Tools

All tools are available to bots during a tick. Tool calls are logged to `bot.log` with level `TOOL` (input) and `OK`/`ERR` (result).

## File System

| Tool | Parameters | Description |
|---|---|---|
| `read_file` | `path` | Read a file. `brain.md` reads the hot brain layer. |
| `read_file_range` | `path`, `start`, `end?` | Read a line range (1-indexed). |
| `write_file` | `path`, `content`, `description?` | Write a file. Files in `entities/` are also indexed. Writing `brain.md` calls `evolve_brain`. |
| `append_file` | `path`, `content` | Append to a file (creates it if absent). |
| `delete_file` | `path` | Delete a file. |
| `replace_in_file` | `path`, `old`, `new` | Replace all occurrences of `old` with `new`. Works on `brain.md`. Uses in-process `scriptling.sed` — no subprocess. |
| `list_dir` | `path?` | List virtual directory. |
| `search` | `pattern`, `path?`, `glob?`, `ignore_case?` | Regex search across files. Returns `file:line:match`. Uses in-process `scriptling.grep`. |

Files outside the bot's own directory are blocked by the scriptling `--allowed-paths` runtime constraint. The `workspace` directory is an explicit exception (mounted and added to allowed paths).

## Shell & Scripts

| Tool | Parameters | Description |
|---|---|---|
| `shell` | `command`, `cwd?`, `timeout?` | Run a shell command via the watchdog proxy. Enforces allowlist + bwrap sandbox. |
| `run_script` | `path`, `args?` | Run a scriptling script. |

`shell` requires the watchdog to be running. `curl`/`wget` are blocked — use `http_request` instead.

## Communication

| Tool | Parameters | Description |
|---|---|---|
| `send_message` | `recipient`, `content` | Direct message by bot ID. Respects communication scope. |
| `read_messages` | — | Drain the inbox (returns all pending messages). |
| `complete_task` | `parent_bot`, `result`, `task_id?` | Report task completion to parent bot. Always allowed regardless of scope. |
| `list_bots` | — | Live swarm view from gossip. Returns bot IDs, goals, scopes, and fitness. |

## Spawning

| Tool | Parameters | Description |
|---|---|---|
| `spawn_bot` | `goal`, `name?`, `brain?`, `model?`, `task_id?` | Create a child bot. Max 10 children per bot. |
| `spawn_hybrid` | `other_bot`, `goal`, `name?`, `model?` | Crossover with another bot's brain and spawn a child. |

## Cognition

| Tool | Parameters | Description |
|---|---|---|
| `evolve_brain` | `content`, `reason?` | Rewrite the hot brain layer (8 KB cap). Takes effect next tick. |
| `query_model` | `model`, `prompt`, `system?`, `thinking?` | One-shot call to any model for a subtask. |
| `list_models` | — | List available models from the catalog. |
| `ask_consensus` | `question`, `n?` | Poll `n` peers (default 3) and return the majority answer. |

## Memory

### Persistent KV Memory

| Tool | Parameters | Description |
|---|---|---|
| `memory_remember` | `content`, `type?`, `importance?`, `reason?` | Store a fact in the persistent KV memory store. |
| `memory_recall` | `query?`, `limit?`, `type?` | Search memories by keyword and similarity. |
| `memory_forget` | `id` | Remove a memory by ID. |

### Warm Memory Layer

| Tool | Parameters | Description |
|---|---|---|
| `recall_warm_memory` | — | Read the full warm memory layer (`memory.md`) into context. |
| `archive_to_warm_memory` | `content` | Append content to the warm memory layer. |
| `update_warm_memory` | `content` | Rewrite the warm memory layer entirely. |

The system prompt shows current warm memory size. Use `recall_warm_memory` when you need archived knowledge; use `archive_to_warm_memory` to offload content from the hot brain layer.

## HTTP

| Tool | Parameters | Description |
|---|---|---|
| `http_request` | `url`, `method?`, `body?`, `content_type?`, `headers?`, `timeout?` | HTTP request (GET/POST/PUT/DELETE/PATCH). Returns `http_status` (real HTTP code). Respects `BOT_HTTP_ALLOWLIST`. |
