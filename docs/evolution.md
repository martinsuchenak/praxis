# Evolution

## Brain

The bot's brain is the content of `brain.md`, injected into the agent's system prompt on every tick. Changes via `evolve_brain` take effect on the very next tick.

The last 5 brain snapshots are stored in `brain_history.json` and surfaced in the system prompt so the bot can see its own evolution trajectory.

### Hot and Warm Memory Layers

The brain has two layers:

- **Hot layer** (`brain.md`) — always in the system prompt. Capped at 8 KB. Holds the bot's active identity, strategy, and skills.
- **Warm layer** (`memory.md`) — on-demand recall up to 50 KB. Holds archived knowledge, past decisions, and reference material the bot doesn't need every tick.

Bots manage both layers explicitly via tools:

| Tool | Purpose |
|---|---|
| `evolve_brain` | Rewrite the hot layer (8 KB cap) |
| `recall_warm_memory` | Read the warm layer into context |
| `archive_to_warm_memory` | Append content to the warm layer |
| `update_warm_memory` | Rewrite the warm layer entirely |

The system prompt shows the current warm memory size so the bot can decide when to recall it.

## Fitness Tracking

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

Fitness counters are visible in the tick message so the LLM can reason about its own progress.

## Self-Replication (`spawn_bot`)

1. Reads own `bot.py` source.
2. Sends a `spawn_req` to the watchdog via gossip with child config.
3. Watchdog writes child `bot.py` and launches it.
4. Child joins the parent's gossip cluster and becomes visible to all peers.

Options:
- `model=` — run the child on a different model than the parent
- `brain=` — give the child a custom starting brain
- `task_id=` — the child's brain will include instructions to call `complete_task` when done

Each bot can create at most 10 children.

## Genetic Crossover (`spawn_hybrid`)

1. Sends a `brain_req` to the target bot via gossip.
2. Target responds with its current brain.
3. Parent merges both brains (via LLM) and spawns a child with the combined brain.
4. Optional `model=` parameter lets the child run on a different model than either parent.

## Task Delegation

A parent can spawn a child with a `task_id`. When the child calls `complete_task(parent_bot, task_id, result)`, a `task_complete` message lands in the parent's inbox and is picked up on the next tick.

## Consensus

Poll `n` peers (any odd number) on a question:

```
ask_consensus(question="Should I prioritise scraping or analysis?", n=3)
```

Each peer answers independently using its own LLM (without thinking mode). The result includes all individual responses, the majority answer, and the agreement ratio. Both the bot and its peers store the exchange in memory.

## Multi-Model Usage

Every bot knows its own model. If a `models.json` catalog is configured, bots also see all available models in their system prompt.

**Model as genome** — `spawn_bot` and `spawn_hybrid` accept an optional `model=` parameter. Children can run on a different model than their parent. Combined with fitness tracking, this enables natural model selection over generations.

**Per-task model routing** — `query_model(model, prompt, system?, thinking?)` sends a one-shot prompt to any model available on the same `base_url`. Useful for cheap summarisation or specialised tasks where the main model is overkill.

See [models.md](models.md) for catalog setup.
