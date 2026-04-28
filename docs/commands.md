# Commands

All commands are run via the `praxis` binary. Global flags apply to all subcommands.

## Global Flags

```
--dir           Praxis project directory (default: ., env: PRAXIS_DIR)
--log-level     Log level: trace|debug|info|warn|error (default: info)
--log-format    Log format: console|json (default: console)
```

## Bot Lifecycle

### spawn

Create a new bot:

```bash
praxis spawn <name> <goal> [flags]
```

Flags:

| Flag | Description |
|---|---|
| `--model` | LLM model name (default: from `BOT_MODEL`) |
| `--brain` | Initial `brain.md` content |
| `--workspace` | Workspace name (must exist in `workspaces.json`) |
| `--scope` | Peer visibility: `open\|isolated\|family\|gateway` |
| `--allowed-workspaces` | Comma-separated workspaces for gateway scope |
| `--parent` | Parent bot ID (for manually wired child bots) |
| `--no-thinking` | Disable thinking mode |

Examples:

```bash
praxis spawn Explorer "Explore and map the codebase"
praxis spawn Worker "Process and summarise data" --model qwen/qwen3-235b-a22b
praxis spawn Scout "Scout environment" --workspace myapp --scope isolated
```

After spawning, start the watchdog (or TUI) to run the bot:

```bash
praxis watchdog
# or
praxis tui
```

### start / stop / kill / restart / remove

```bash
praxis start   <name>   # launch the bot process
praxis stop    <name>   # graceful stop (sets stop flag; picked up next tick)
praxis kill    <name>   # immediate SIGTERM
praxis restart <name>   # kill + start
praxis remove  <name>   # kill + delete bot directory entirely
```

### Bulk operations

```bash
praxis start-all       # start all stopped bots
praxis stop-all        # graceful stop all running bots
praxis kill-all        # SIGTERM all bots
praxis restart-stale   # restart all bots flagged STALE
```

## Inspection

### list

Show all bots with local status (reads `status.json`). Flags STALE bots whose last tick is older than `BOT_STALE_THRESHOLD`.

```bash
praxis list
```

### status

Live swarm view via gossip — includes fitness counters and cluster membership. Requires the watchdog to be running.

```bash
praxis status
```

### logs

Print recent lines from a bot's `bot.log`:

```bash
praxis logs <name>          # last 40 lines
praxis logs <name> --lines 100
```

### tail

Follow a bot's log in real time:

```bash
praxis tail <name>          # follow bot.log
praxis tail <name> output   # follow output.log (stdout/stderr)
```

## Communication

### send

Deliver a message to a running bot's inbox (picked up on the next tick, `from: operator`):

```bash
praxis send <name> "Your message here"
```

## Export / Import

### export

Package a bot into a portable archive for transfer to another machine:

```bash
praxis export <name>
praxis export <name> --output /tmp/explorer.tar.gz
```

The archive contains the bot directory, the `praxis` binary, `.env.example`, and a `bootstrap.sh` launcher.

### import

Extract a bot archive, optionally remapping workspace paths:

```bash
praxis import explorer.tar.gz
praxis import explorer.tar.gz --workspace myapp=/home/user/projects/myapp
praxis import explorer.tar.gz --name ExplorerV2
```

The `--workspace` flag can be repeated and maps workspace names to local paths. Remappings are applied to the bot's `status.json` on import.

## Runtime

### watchdog

Start the gossip node, bot runner pool, and crash-recovery loop (headless):

```bash
praxis watchdog [flags]
```

Flags:

| Flag | Env | Default | Description |
|---|---|---|---|
| `--port` | `BOT_WATCHDOG_PORT` | `7700` | Gossip listen port |
| `--advertise` | `BOT_WATCHDOG_ADDR` | `0.0.0.0:<port>` | Gossip advertise address |
| `--seeds` | `BOT_SEED_ADDRS` | — | Comma-separated seed peer addresses |
| `--secret` | `BOT_GLOBAL_SECRET` | — | Global gossip secret |
| `--sandbox` | `BOT_SHELL_SANDBOX` | `auto` | Sandbox mode: `auto\|bwrap\|none` |
| `--mounts` | `BOT_SHELL_MOUNTS` | — | Extra sandbox mounts |

The watchdog joins the gossip cluster as `role=watchdog`. It:
- Monitors bot processes and auto-restarts crashed bots
- Proxies `shell` commands from bots (enforces allowlist + bwrap sandbox)
- Relays cross-workspace messages for gateway-scoped bots
- Handles `spawn` requests sent by bots via gossip

### tui

Start an interactive TUI dashboard with bot list, live log streaming, and slash commands. Accepts the same flags as `watchdog`.

```bash
praxis tui [flags]
```

Slash commands available in the TUI:

| Command | Description |
|---|---|
| `/select <name>` | Switch log view to named bot |
| `/start <name>` | Start a bot |
| `/stop <name>` | Graceful stop |
| `/kill <name>` | SIGTERM |
| `/restart <name>` | Kill + start |
| `/spawn <name> <goal>` | Spawn a new bot |
| `/logs <name>` | Show last log lines for a bot |
| `/exit` | Exit the TUI |

Typing plain text (without a `/` prefix) delivers the message to the currently selected bot's inbox.
