# Praxis — Go Rewrite Plan

Rebuild the scriptling-based control plane (`bin/control.py`) as a compiled Go binary
(`praxis`) that embeds scriptling, owns the gossip watchdog, enforces the security
model at the API level, and provides an interactive TUI dashboard.

The bot scripts (`lib/botcore.py`) remain scriptling. What changes is only the
scaffolding that surrounds them.

---

## Motivation

The current control plane runs as a scriptling script that simultaneously manages OS
processes, participates in a gossip cluster, proxies shell commands, and injects config
into bot source files via string markers. The result is a set of structural problems:

- `_inject_config` is duplicated between `bin/control.py` and `lib/botcore.py`
- Bot config is baked into source via marker replacement — fragile and hard to audit
- The `workspace_path` field is read from bot-writable `status.json` to compute
  `--allowed-paths`, creating a real privilege escalation path
- No live dashboard — watching bots requires multiple terminal windows
- Crash recovery requires a separate watchdog process that is itself a scriptling script

---

## Libraries

| Package | Role |
|---|---|
| `github.com/paularlott/cli` | CLI commands + TUI dashboard |
| `github.com/paularlott/cli/tui` | Multi-panel terminal UI |
| `github.com/paularlott/gossip` | Native Go gossip cluster (watchdog node) |
| `github.com/paularlott/scriptling` | Embedded scriptling interpreter per bot |
| `github.com/paularlott/scriptling/extlibs/container` | Optional container tool for bots (Docker/Podman/Apple) |
| `github.com/paularlott/logger` | Structured logging |

---

## Architecture Overview

```
praxis (single Go binary)
│
├── One-shot CLI commands   spawn, list, logs, tail, send, status,
│   (file + gossip based)   export, import — work with or without a
│                           running instance for file operations;
│                           send/status join gossip temporarily.
│
└── Long-running mode       One of these must be running whenever bots
    (choose one)            are running — it IS the watchdog.
    │
    ├── watchdog            Headless supervisor: gossip node + bot runner
    │                       pool + crash recovery + structured log output.
    │
    └── tui                 Same as watchdog but with interactive TUI
                            dashboard instead of plain log output.
```

**The watchdog must always be running when bots are active.** Without it:
- Shell proxy is unavailable → bots cannot execute shell commands
- Spawn arbitration is unavailable → bots cannot create children
- Crash recovery does not happen

`praxis watchdog` and `praxis tui` are the two entry points for this role.
Exactly one instance runs at a time (it owns `BOT_WATCHDOG_PORT`). One-shot
commands detect whether a watchdog is running and warn if not.

Bot scripts (`botcore.py`) run inside the long-running process via
`scriptling.Clone()` + `EvalWithContext`. Each clone gets:
- `extlibs.RegisterOSLibrary(clone, allowedPaths)` — filesystem restriction
- Subprocess library **not** registered — bots cannot exec directly
- Config pre-set via `SetVar` — no source-level injection

Shell commands flow through the gossip proxy → pluggable sandbox (bwrap by
default). Bots running without `praxis` lose shell access and child
spawning — by design; `praxis` ships with every export.

---

## Project Structure

```
praxis/
  cmd/
    root.go             — CLI root, logger init, .env loading (cli/env)
    spawn.go            — spawn command
    start.go            — start / start-all
    stop.go             — stop / stop-all
    kill.go             — kill / kill-all
    restart.go          — restart / restart-stale
    list.go             — list (file-based, no gossip needed)
    status.go           — status (live swarm view via gossip)
    send.go             — send message to a running bot
    logs.go             — logs / tail
    export.go           — package bot into portable archive
    importcmd.go        — import archive, remap workspace paths
    watchdog.go         — start gossip node + bot runner pool
    tui.go              — interactive TUI dashboard
  internal/
    bot/
      config.go         — BotConfig type, load/save config.json (controller-owned)
      state.go          — BotState type, load/save state.json (bot-writable)
      manager.go        — create, list, delete bots; read both files
      runner.go         — goroutine lifecycle: run loop, stop, kill, backoff
      template.go       — copy botcore.py → bot.py on first create (no injection)
    cluster/
      cluster.go        — gossip.Cluster setup, lifecycle, metadata
      proxy.go          — MSG_SHELL_REQ handler: allowlist, bwrap, execute
      spawn.go          — MSG_SPAWN_REQ handler: validate + privilege inheritance
      relay.go          — MSG_RELAY_REQ handler: scope/workspace checks
    sandbox/
      sandbox.go        — Sandbox interface + ExecOptions/ExecResult types
      factory.go        — NewSandbox(cfg): selects implementation from env var
      bwrap.go          — BwrapSandbox implementation
      nosandbox.go      — NoSandbox (fallback / dev)
      paths.go          — AllowedPaths(config) and InheritPaths(parent, child)
    tui/
      dashboard.go      — layout wiring, command registration, event loop
      botlist.go        — left panel: renders bot list, reacts to gossip events
      logview.go        — right panel: tails bot.log, streams to panel
  lib/                  — (symlinked or copied from ../lib at build time)
  main.go
  go.mod
  Taskfile.yml
```

---

## Config / State Split

Every bot directory has two JSON files with clearly separated ownership:

```
bots/Devbot/
  config.json       ← controller writes ONLY
                      name, goal, model, thinking,
                      workspace, workspace_path, scope,
                      allowed_workspaces, parent, gossip_secret,
                      created_at

  state.json        ← bot writes (via scriptling OS library)
                      status, gossip_addr, fitness, last_tick_ts,
                      last_activity, is_leader

  bot.py            ← bot writes (self-evolution)
  brain.md          ← bot writes
  bot.log           ← bot writes
  entities/         ← bot writes
```

`config.json` is **not** in the bot's `allowedPaths`. The scriptling OS library
will refuse any read or write to it. Privilege escalation via config tampering
is structurally impossible — there is no runtime check to bypass.

On restart the controller reads `config.json`, constructs `allowedPaths`, and
injects config into the cloned interpreter via `SetVar`. The bot never has a
path to its own security config.

---

## Scriptling Interpreter Setup

One base interpreter created at startup with shared library registrations.
Each bot gets an isolated clone:

```go
func (r *Runner) newInterpreter() *scriptling.Interpreter {
    p := r.base.Clone()
    extlibs.RegisterOSLibrary(p, r.config.AllowedPaths())
    // subprocess library intentionally NOT registered
    p.SetVar("CONFIG", r.config.AsDict())
    p.SetVar("DEFAULTS", r.defaults)
    p.SetVar("SYSTEM_PROMPT", r.systemPrompt)
    p.SetVar("AVAILABLE_MODELS", r.models)
    p.SetSourceFile(r.botPyPath)
    return p
}
```

Runner goroutine loop:

```go
func (r *Runner) run(ctx context.Context) {
    for {
        p := r.newInterpreter()
        script, err := os.ReadFile(r.botPyPath)
        if err != nil {
            r.log.Error("cannot read bot.py", "err", err)
            return
        }
        _, err = p.EvalWithContext(ctx, string(script))
        if ctx.Err() != nil {
            return  // clean stop or kill
        }
        if err != nil {
            r.log.Error("bot crashed", "id", r.config.Name, "err", err)
        }
        select {
        case <-ctx.Done():
            return
        case <-time.After(r.nextBackoff()):
        }
    }
}
```

Self-evolution works unchanged: the bot writes `bot.py` via the scriptling OS
library (restricted to its own directory). On the next goroutine loop iteration
the new file is loaded. The allowed paths for the new interpreter are computed
from `config.json` — which the bot cannot modify.

---

## Gossip Architecture

The controller binds one gossip cluster at `BOT_WATCHDOG_PORT`. Three message
types use `HandleFuncWithReply`:

### MSG_SHELL_REQ

```
Bot → watchdog: {type, bot_id, command, cwd, timeout, _secret}
Watchdog → Bot: {exit_code, stdout, stderr}
```

Handler checks:
1. Secret valid (against workspace secret or global secret)
2. `bot_id` valid name, directory exists
3. First word not in `_SHELL_BLOCKED` (`curl`, `wget`)
4. First word in allowlist (if allowlist configured)
5. Delegate to injected `Sandbox.Execute(ctx, opts)` — implementation chosen at startup
6. Return result

### MSG_SPAWN_REQ

```
Bot → watchdog: {type, name, goal, model, brain, thinking, _secret}
Watchdog → Bot: {bot_id: "..."} or {error: "..."}
```

Handler enforces privilege inheritance:

```go
func validateSpawnPrivileges(parent *BotConfig, req SpawnRequest) error {
    // requested workspace must be in parent's allowed_workspaces or parent's own workspace
    // requested scope must not exceed parent's scope
    // resulting allowed_workspaces ⊆ parent's allowed_workspaces
}
```

Child `config.json` is written by the controller with inherited paths. Bot gets
back the child ID. BOTS_DIR write access is not needed.

### MSG_RELAY_REQ

```
Bot → watchdog: {type, from, target_bot, content, _secret}
Watchdog → Bot: {status: "relayed"} or {error: "..."}
```

Checks source scope == "gateway", target workspace ∈ source.allowed_workspaces.

### Membership events → TUI

```go
cluster.HandleNodeStateChangeFunc(func(node *gossip.Node, old, new gossip.NodeState) {
    dashboard.OnNodeStateChange(node, new)
})
cluster.HandleNodeMetadataChangeFunc(func(node *gossip.Node) {
    dashboard.OnNodeMetadataChange(node)
})
```

---

## Sandbox Abstraction

The shell proxy executes commands through a pluggable `Sandbox` interface, making
bwrap swappable without touching the proxy handler.

```go
// internal/sandbox/sandbox.go

type ExecOptions struct {
    Command       string
    BotDir        string
    CWD           string
    WorkspacePath string
    Timeout       time.Duration
}

type ExecResult struct {
    ExitCode int
    Stdout   string
    Stderr   string
}

type Sandbox interface {
    Execute(ctx context.Context, opts ExecOptions) (*ExecResult, error)
    Available() bool   // false → fall through to next in chain or NoSandbox
    Name() string
}
```

Implementations live in `internal/sandbox/`:

| File | Type | Notes |
|---|---|---|
| `bwrap.go` | `BwrapSandbox` | Linux namespaces via bwrap; ported from control.py |
| `nosandbox.go` | `NoSandbox` | Plain exec, no isolation; dev/fallback only |

Selected via `BOT_SHELL_SANDBOX` env var:

| Value | Behaviour |
|---|---|
| `bwrap` (default) | Use bwrap; fatal error at startup if not available |
| `none` | No sandboxing; logs a warning at startup |
| _(auto)_ | Try bwrap; fall back to `none` with a warning |

The proxy handler receives a `Sandbox` via constructor injection — it never
reads the env var directly. This makes the handler fully testable with a mock
sandbox and keeps the selection logic in one place at startup.

`sandbox/paths.go` — two functions:

```go
// AllowedPaths returns the filesystem paths the bot may access.
// Derived solely from config.json — never from state.json or bot.py.
func AllowedPaths(cfg *BotConfig, botsDir, locksDir string) []string

// InheritPaths enforces that child paths ⊆ parent paths.
// Returns an error if the child requests anything outside the parent's set.
func InheritPaths(parent, child []string) ([]string, error)
```

Scope ordering for spawn validation:

```
open > family > isolated > gateway
```

A child may only be spawned with a scope equal to or more restrictive than its parent.

---

## Container Tool (Optional Bot Capability)

`scriptling.container` gives bots the ability to manage Docker/Podman/Apple
containers — pull images, run containers, exec commands, manage volumes. This
is a tool *for bots to use in their work* (e.g. running tests, building images),
not sandboxing of the bots themselves.

Registered at startup if `BOT_CONTAINER_ENABLED=true`:

```go
// in runner setup
if cfg.ContainerEnabled {
    container.Register(base, cfg.DockerSocket, cfg.PodmanSocket)
}
```

Bots then import `scriptling.container` and call:

```python
import scriptling.container as ct
c = ct.client()          # auto-detects available runtime
c.pull("alpine:latest")
cid = c.run("alpine:latest", command=["echo", "hello"])
result = c.exec(cid, ["cat", "/etc/os-release"])
c.stop(cid)
c.remove(cid)
```

New env vars:

| Variable | Default | Description |
|---|---|---|
| `BOT_CONTAINER_ENABLED` | `false` | Register the container library for bots |
| `BOT_CONTAINER_RUNTIME` | _(auto)_ | Force `docker`, `podman`, or `apple`; auto-detects if unset |
| `BOT_CONTAINER_DOCKER_SOCK` | `/var/run/docker.sock` | Override Docker socket path |
| `BOT_CONTAINER_PODMAN_SOCK` | `/var/run/podman.sock` | Override Podman socket path |

The container library is registered on the **base** interpreter so all bot clones
inherit it when enabled. No per-bot configuration needed.

---

## TUI Dashboard

```
┌─────────────────────┬────────────────────────────────────────────┐
│  BOTS         [3/3] │  Devbot — bot.log                          │
│                     │                                            │
│ ● Devbot  RUNNING   │  05:31:22 [TICK] 42                        │
│   ticks=42  brain=7 │  05:31:22 [TOOL] list_dir path=src         │
│                     │  05:31:22 [OK]   12 entries                │
│ ● Buildy  RUNNING   │  05:31:23 [TOOL] shell command=go build .  │
│   ticks=18          │  05:31:24 [OK]   {"exit_code":0}           │
│                     │  05:31:55 [TOOL] write_file path=report.md │
│ ○ Tester  STOPPED   │  05:31:55 [OK]   Written                   │
│                     │                                            │
├─────────────────────┴────────────────────────────────────────────┤
│ > /send please review the output and update brain                │
└──────────────────────────────────────────────────────────────────┘
```

**Left panel** (`SetContent` on gossip events + state.json poll):
- Bot name, status badge (`●` running / `○` stopped / `⚠` stale)
- Tick count, fitness summary

**Right panel** (`WriteString` as log lines arrive):
- File watcher on selected bot's `bot.log`
- Streams new content; `ScrollToBottom` on new lines unless user has scrolled up

**Input** (bottom):
- Plain text → gossip message to selected bot
- `/command` → registered TUI commands

**Registered slash commands:**

| Command | Effect |
|---|---|
| `/select <bot>` | Switch right panel to that bot's log |
| `/spawn <name> "<goal>" [key=val]` | Spawn new bot (shows prompts for required fields) |
| `/start [bot]` | Start selected or named bot |
| `/stop [bot]` | Graceful stop |
| `/kill [bot]` | Immediate kill |
| `/restart [bot]` | Kill + start |
| `/logs` | Scroll right panel to top |
| `/status` | Open menu overlay with full swarm view |

Bot selection also via `OpenMenu` — arrow-key navigation, Enter switches log view.

---

## Changes to botcore.py

Only structural scaffolding changes. All tool logic, tick loop, brain/memory/entities,
consensus, and model locking are unchanged.

### Removed

- All injection marker blocks:
  `# --- BOT CONFIG ---`, `# --- DEFAULTS ---`, `# --- SYSTEM PROMPT ---`,
  `# --- MODELS ---` and their `# --- END ... ---` counterparts
- `_inject_config` function (spawn goes via gossip to controller)
- `_spawn_bot` direct filesystem writes

### Changed

- `CONFIG`, `DEFAULTS`, `SYSTEM_PROMPT`, `AVAILABLE_MODELS` are pre-set by the
  controller via `SetVar` before `EvalWithContext`. botcore.py reads them as
  normal variables — no change to any code that *uses* them.
- `_spawn_bot`: replace direct file creation with a gossip `spawn_req` sent to
  the watchdog node using `send_message_with_reply`. Same return contract:
  returns the new bot ID on success or an error string.

### Net change

Approximately 80 lines removed from botcore.py, zero logic changes.

---

## Testing Strategy

### Unit tests (`-short` flag, no external dependencies)

**`internal/bot/`**

| File | Tests |
|---|---|
| `config_test.go` | Load from JSON, save atomic (tmp+rename), round-trip, missing fields get defaults |
| `state_test.go` | Load/save state.json, partial update preserves other fields |
| `template_test.go` | Generated bot.py contains no injection markers, is valid UTF-8, CONFIG placeholder present |

**`internal/sandbox/`**

| File | Tests |
|---|---|
| `paths_test.go` | No workspace → [botDir, locksDir]; with workspace → includes workspace_path; child paths ⊆ parent; child requesting extra path returns error; empty parent set propagates |
| `bwrap_test.go` | Inner CWD for path inside botDir; inner CWD for path outside botDir falls back to `/`; BOT_SHELL_MOUNTS parsing: ro/rw modes, missing host path skipped; `Available()` false when bwrap not in PATH |
| `nosandbox_test.go` | `Available()` always true; executes command; timeout respected |
| `factory_test.go` | `BOT_SHELL_SANDBOX=bwrap` returns BwrapSandbox; `=none` returns NoSandbox; auto-select returns BwrapSandbox when available, NoSandbox otherwise |

**`internal/cluster/`**

| File | Tests |
|---|---|
| `spawn_test.go` | Child workspace in parent.allowed_workspaces → allowed; child workspace not in list → error; child scope more permissive than parent → error; child scope equal → allowed; child scope more restrictive → allowed; nil parent config → error |
| `proxy_test.go` | Blocked command (curl) rejected; command not in allowlist rejected; command in allowlist allowed; allowlist empty → any non-blocked command allowed; invalid bot_id format rejected; secret mismatch rejected; proxy calls `Sandbox.Execute` with correct options (mock sandbox) |
| `relay_test.go` | Source scope != gateway → error; target workspace not in allowed_workspaces → error; valid relay → routed |

### Integration tests (`internal/integration/`)

Run with `-run Integration` or without `-short`. Require `scriptling` in PATH
and optionally `bwrap` (skipped gracefully when absent).

| File | Tests |
|---|---|
| `bot_lifecycle_test.go` | Start bot → reaches running state; graceful stop; kill; crash recovery restarts within backoff window |
| `spawn_privilege_test.go` | Bot cannot spawn child with workspace outside parent's set; bot cannot spawn child with broader scope; valid child spawn succeeds and inherits paths |
| `shell_proxy_test.go` | Bot shell command executes via proxy with NoSandbox; same test repeated with BwrapSandbox when bwrap available; blocked command returns error; timeout respected |
| `export_import_test.go` | Export produces valid archive; import extracts + remaps workspace_path; bot starts cleanly after import |

### Test helpers

```
internal/testutil/
  tempbot.go      — create a temporary bot directory with valid config.json + state.json
  fakecluster.go  — in-process gossip cluster pair for handler tests
  mocksandbox.go  — Sandbox implementation that records calls and returns fixtures
  assertions.go   — WaitForStatus(id, status, timeout) and similar helpers
```

---

## Taskfile

See `praxis/Taskfile.yml`. Key tasks:

| Task | Description |
|---|---|
| `task build` | Build release binary to `bin/praxis` |
| `task build:debug` | Build with debug symbols |
| `task test` | All tests |
| `task test:unit` | Unit tests only (`-short`) |
| `task test:race` | Tests with `-race` detector |
| `task test:cover` | Coverage report → `coverage.html` |
| `task lint` | `golangci-lint run ./...` |
| `task vet` | `go vet ./...` |
| `task check` | vet + lint + test (CI gate) |
| `task install` | `go install` to `GOPATH/bin` |
| `task tidy` | `go mod tidy` |
| `task clean` | Remove `bin/`, coverage files |

---

## Phased Implementation

### Phase 1 — Skeleton ✅
Module init, CLI root with logger + `.env` loading, all commands stubbed.

**Deliverable**: `praxis --help` shows all commands. ✅

### Phase 2 — File-based bot management ✅
`internal/bot/` fully implemented. `config.json` / `state.json` split in place.
Template generation (copy botcore.py, no injection). Commands: `spawn`, `list`,
`logs`, `tail`, `remove`, `start`, `stop`, `kill`, `restart`, `restart-stale`.
`internal/sandbox/` fully implemented: `Sandbox` interface, `BwrapSandbox`,
`NoSandbox`, `factory.go`, `paths.go`. 47 unit tests, all passing.

**Deliverable**: Can create and inspect bots without running any bot. ✅

### Phase 3 — Gossip + shell proxy ✅
`internal/cluster/` fully implemented: `messages.go` (ShellRequest/SpawnRequest/
RelayRequest structs), `cluster.go` (Node wrapping gossip.Cluster), `proxy.go`
(shell_req handler with sandbox injection + block list), `spawn.go` (spawn_req
handler with scope + workspace privilege inheritance), `relay.go` (relay_req
handler with gateway-scope check), `secrets.go` (bot/global secret validation),
`deliver.go` (file-based inbox delivery). `watchdog` command starts gossip node
with configurable sandbox. `status` and `send` commands implemented.
15 cluster unit tests, all passing. Total: 62 unit tests.

**Deliverable**: Watchdog gossip node runs; shell, spawn, relay requests handled
with full privilege enforcement. Sandbox can be swapped via config. ✅

### Phase 4 — Embedded scriptling runner ✅
`internal/bot/runner.go` implemented: `Runner` (single-bot goroutine with
crash backoff), `RunnerPool` (multi-bot lifecycle manager), `newBaseInterpreter`
(shared base with stdlib + extlibs registered). Per-bot OS library registered
on clone with config-derived allowed paths. Watchdog command wires pool, auto-
starts existing bots, and runs a 500ms monitor loop for file-based signals.
8 runner tests (nextBackoff, start/stop/kill, crash+restart, context cancel).

**Deliverable**: Full bot lifecycle managed by Go binary. 82 unit tests. ✅

### Phase 5 — Spawn arbitration ✅
`MSG_SPAWN_REQ` handler implemented with privilege inheritance enforcement.
`_spawn_bot` in botcore.py updated to send gossip `spawn_req` to watchdog
instead of writing files directly. Bot no longer needs BOTS_DIR write access.
Dispatcher refactored: single handler at MsgBotToWatchdog (128) routes by
"type" field, matching botcore.py's GOSSIP_MSG = gossip.MSG_USER convention.

**Deliverable**: Child bots can be spawned; privilege escalation blocked. ✅

### Phase 6 — TUI dashboard ✅
`internal/tui/` implemented (`dashboard.go`, `deliver.go`). `tui` command
starts the full dashboard with the same watchdog setup as the headless command.
Left panel shows bot list with status/tick counts (500ms refresh). Main panel
streams the selected bot's `bot.log` (200ms poll). Slash commands: /select,
/start, /stop, /kill, /restart, /spawn, /logs, /exit. Plain text input sends
to selected bot's inbox.

**Deliverable**: Interactive dashboard replaces `tail` + multiple terminals. ✅

### Phase 7 — Export / import with workspace remapping ✅
`export` packages bot dir + `praxis` binary + `.env.example` + `bootstrap.sh`
into a `.tar.gz` archive. `import` extracts, maps workspace names to local
paths (--workspace name=/path), writes remapped `config.json`.

**Deliverable**: Bots can migrate between machines cleanly. ✅

### Phase 8 — Remove old control plane ✅
Removed all injection marker blocks (`# --- BOT CONFIG ---`, `# --- DEFAULTS ---`,
`# --- MODELS ---`, `# --- SYSTEM PROMPT ---` and their END counterparts).
Removed `_inject_config` function. `_spawn_bot` already updated in Phase 5.
Deleted `bin/control.py`. Moved Go code to repo root (go.mod, main.go, cmd/,
internal/ all at repo root alongside lib/). `lib/botcore.py` embedded into
the binary via `//go:embed`; `manager.copyTemplate` uses the embedded bytes
so `praxis` is self-contained and works from any directory.

**Deliverable**: Old scriptling control plane gone. Migration complete. ✅

---

## Invariants That Must Hold Throughout

1. A bot cannot read or write its own `config.json` — enforced by the scriptling
   OS library's allowed path list, not by runtime checks.
2. A child bot's allowed paths are always a subset of its parent's — enforced
   by `InheritPaths` in the spawn handler, which the bot never executes directly.
3. Self-evolution (rewriting `bot.py`) cannot change the bot's allowed paths —
   paths are computed from `config.json` at interpreter construction, never
   from bot-writable files.
4. Shell commands run through the proxy only — bots have no direct subprocess
   access because the subprocess library is not registered in their interpreter.
5. `praxis` is always present — standalone `scriptling bot.py` is an
   unsupported configuration that deliberately lacks shell and spawn capability.
6. Exactly one `praxis` instance runs at a time — it owns `BOT_WATCHDOG_PORT`
   and is the single source of truth for bot lifecycle. One-shot commands
   communicate via file writes and temporary gossip joins; they never start a
   second long-running instance.
7. The `Sandbox` implementation is chosen once at startup and injected — the
   proxy handler never reads env vars directly, keeping sandbox selection
   testable and swappable without code changes.
