# Model Catalog

An optional `models.json` in the project root lets bots reason about and use multiple LLM models.

## Format

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
    "strengths": ["reasoning", "complex analysis", "planning", "architecture"],
    "concurrency": 2
  }
]
```

Fields:

| Field | Required | Description |
|---|---|---|
| `id` | yes | Model name as accepted by your API endpoint |
| `label` | yes | Human-readable name |
| `description` | yes | What the model is good for |
| `cost` | yes | `low`, `medium`, or `high` |
| `strengths` | yes | Tag list for the bot to reason about model selection |
| `concurrency` | no | Max simultaneous LLM calls for this model (overrides `BOT_MAX_CONCURRENT`) |

## Effect on Bots

When `models.json` is present, bots get an **Available Models** section in their system prompt and two additional tools:

- `list_models` — returns the full catalog
- `query_model(model, prompt, system?, thinking?)` — one-shot call to any model on the same `base_url`

The catalog is baked into each bot's `bot.py` at spawn time so child and migrated bots carry it forward.

If `models.json` is absent, `list_models` reports no models available.

## Per-Model Concurrency

Before each LLM call, bots acquire a concurrency slot under `.locks/<model>/`. Slots held longer than the request timeout are treated as stale (handles crashes). This prevents slow models from being hammered by concurrent requests.

`concurrency` in `models.json` sets the limit per model; `BOT_MAX_CONCURRENT` is the global fallback.
