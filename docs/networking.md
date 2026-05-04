# Networking

## Discovery

### Bots

On startup each bot:

1. Tries any `seed_addrs` from its CONFIG to join an existing gossip cluster.
2. Falls back to multicast on `239.255.13.37:19373` — sends a discover, waits 3 s for an announce from a peer.
3. If nothing responds, starts as the root node of a new swarm.
4. Broadcasts a multicast announce so future bots can find it.

Every 10 ticks (≈5 min) each bot re-announces on multicast, so bots that start later can join.

### Watchdog Nodes

When `--seeds` is provided, watchdogs join explicitly via those seed addresses.

When no seeds are configured, watchdogs use **multicast auto-discovery** on `239.255.13.37:19373` (same group/port as bots):

1. The watchdog listens on the multicast group and periodically broadcasts discover messages.
2. When it hears another watchdog, it joins its gossip cluster.
3. Discovery stops once the first peer is found (subsequent peers propagate via gossip membership).

This allows multiple `praxis watchdog` (or `praxis tui`) instances on the same network to find each other without manual configuration. Disable by providing `--seeds` or setting `BOT_MULTICAST_ADDR=""`.

## Gossip Cluster

Once a bot joins a cluster, membership and metadata propagate automatically across all machines. Each bot publishes its ID, goal, workspace, scope, and gossip address as metadata. `list_bots` returns the live cluster view — no shared file needed.

The watchdog node joins with `role=watchdog`. Bots register with `role=bot` and join a criteria-based group `{"role": "bot"}` so tools like `list_bots` only see bot peers, not the watchdog.

## Message Types

All inter-bot messages are sent via `gossip.send_to()`. Request/reply patterns use `gossip.send_request()` / `handle_with_reply()`.

| `type` | Direction | Purpose |
|---|---|---|
| `message` | one-way | Direct message; delivered to recipient inbox |
| `brain_req` | request/reply | Request another bot's brain for genetic crossover — reply: `{"brain": "..."}` |
| `consensus_req` | request/reply | Ask a peer to answer a question — reply: `{"answer": "...", "from": "..."}` |
| `task_complete` | one-way | Child bot reporting task completion to parent |
| `stop` | one-way | Remote graceful stop signal |
| `shell_req` | request/reply | Bot → watchdog command proxy — reply: `{"exit_code": ..., "stdout": ..., "stderr": ...}` |
| `relay_req` | request/reply | Bot → watchdog cross-workspace relay — reply: `{"status": "relayed"}` or `{"error": ...}` |
| `relayed_message` | one-way | Watchdog → bot cross-workspace message — contains `from`, `content` |
| `spawn_req` | request/reply | Bot → watchdog spawn request — reply: `{"status": "spawned"}` or `{"error": ...}` |
| `remote_spawn_req` | request/reply | Watchdog → watchdog remote spawn — creates a bot on the target node |
| `terminate_req` | request/reply | Bot → watchdog self-termination request — reply: `{"status": "terminated"}` or `{"error": ...}` |
| `hardware_req` | request/reply | Bot requests the watchdog to route a command to a hardware device node. Fields: node, peripheral, affordance, operation, input. |

## Communication Scope

Bots have a **scope** that controls which peers they can see and message. Scope is set at spawn time and cannot be changed at runtime.

| Scope | Visibility | Cross-workspace |
|---|---|---|
| `open` | All bots on the gossip network | Direct messaging |
| `isolated` | Same-workspace bots only | None |
| `gateway` | Same-workspace + allowed workspaces | Via watchdog relay |
| `family` | Parent bot and its direct children only | None |

How it works:

- Each bot publishes `scope` and `workspace` as gossip metadata.
- `list_bots` and `send_message` filter peers by scope rules at the tool level.
- Gateway bots can send messages to bots in `allowed_workspaces` — the watchdog relays the message on their behalf. The target receives it as a `relayed_message`.
- Incoming consensus requests and relayed messages always reach a bot regardless of scope.
- Per-workspace `gossip_secret` provides application-level message filtering — bots with different secrets drop each other's messages.
- Scope is read from `status.json` into a module variable at startup.

## Leader Election

A leader election runs automatically with 51% quorum among `role=bot` nodes. The elected leader sees `"You are the swarm leader."` in its tick message. Leader status is also shown in `praxis list` and `praxis status`.

Leader-specific behaviour can be added to bot brains (e.g. coordination tasks, health checks, triage).

## Authentication

Set `BOT_GLOBAL_SECRET` (or `gossip_secret` per workspace in `workspaces.json`) to authenticate inter-bot messages. All messages include `_secret` in the payload; unauthenticated messages are dropped.

Bots on different machines need the same secret in their `.env`.
