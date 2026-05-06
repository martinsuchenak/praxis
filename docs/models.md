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
    "strengths": ["fast", "simple tasks", "formatting", "summaries"],
    "thinking_template": "qwen"
  },
  {
    "id": "glm-5",
    "label": "GLM 5",
    "description": "General-purpose model with strong tool calling.",
    "cost": "medium",
    "strengths": ["tool calling", "instruction following"],
    "thinking_template": "glm"
  },
  {
    "id": "claude-sonnet-4-6",
    "label": "Claude Sonnet 4.6",
    "description": "Strong general-purpose reasoning with adaptive thinking.",
    "cost": "high",
    "strengths": ["reasoning", "writing", "analysis"],
    "thinking_template": "anthropic"
  },
  {
    "id": "granite-4.1:3b-instruct",
    "label": "Granite 4.1 3B",
    "description": "Function calling and RAG. No thinking support.",
    "cost": "low",
    "strengths": ["function calling", "RAG"]
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
| `thinking_template` | no | Reference a built-in thinking template by name (see below) |
| `thinking` | no | Inline thinking config — overrides `thinking_template` if both are present |

## Thinking Configuration

Controls how the bot enables or disables a model's reasoning/thinking mode. Models without a `thinking` or `thinking_template` field send prompts as-is with no thinking control.

### Built-in Templates

Common provider templates are built into the bot runtime. Reference them by name with `thinking_template`:

| Template | Providers | Description |
|---|---|---|
| `qwen` | Qwen3 via LM Studio, any `/no_think` model | Prefix mode: prepends `/no_think` |
| `ollama` | Ollama native API | JSON body: `{"think": true/false}` |
| `ollama_compat` | Ollama OpenAI-compatible API | JSON body: `{"reasoning_effort": "high"/"none"}` |
| `openai` | OpenAI o3, o4-mini, gpt-5.x | JSON body: `{"reasoning_effort": "high"/"none"}` |
| `anthropic` | Claude Sonnet/Opus | JSON body: `{"thinking": {"type": "adaptive"/"disabled"}}` |
| `glm` | GLM-5, GLM-5.1 (Zhipu AI) | JSON body: `{"thinking": {"type": "enabled"/"disabled"}}` |
| `gemini_flash` | Gemini 2.5 Flash | JSON body: `{"generationConfig": {"thinkingConfig": {"thinkingBudget": ...}}}` |
| `mistral` | Mistral Small, Medium 3.5 | JSON body: `{"reasoning_effort": "high"/"none"}` |

Usage:

```json
{"id": "glm-5", "thinking_template": "glm"}
```

### Inline Override

Use the `thinking` field to define thinking config inline. This overrides `thinking_template` if both are present.

#### `prefix` mode

Prepends a text prefix to the prompt to disable thinking. Used for models accessed through providers that don't support API-level thinking parameters.

```json
{
  "thinking": {
    "mode": "prefix",
    "prefix": "/no_think"
  }
}
```

When the bot's `Thinking` flag is `false`, the prefix is prepended to the prompt. When `true`, the prompt is sent unmodified.

#### `json_body` mode

Passes extra JSON body parameters to the API call. Used for providers with proper API-level thinking controls.

```json
{
  "thinking": {
    "mode": "json_body",
    "body_on": {
      "thinking": {"type": "enabled", "clear_thinking": false}
    },
    "body_off": {
      "thinking": {"type": "disabled"}
    }
  }
}
```

`body_on` is merged into the API request body when the bot's `Thinking` flag is `true`. `body_off` is used when `false`.

### Resolution Order

1. If `thinking` is present on the model entry → use it
2. Else if `thinking_template` references a built-in template → use it
3. Else → no thinking control, prompt sent as-is

It is your responsibility to configure the correct template or inline config for your provider and endpoint. The bot follows the `models.json` configuration exactly.

## Effect on Bots

When `models.json` is present, bots get an **Available Models** section in their system prompt and two additional tools:

- `list_models` — returns the full catalog
- `query_model(model, prompt, system?, thinking?)` — one-shot call to any model on the same `base_url`

The catalog is baked into each bot's `bot.py` at spawn time so child and migrated bots carry it forward.

If `models.json` is absent, `list_models` reports no models available.

## Per-Model Concurrency

Before each LLM call, bots acquire a concurrency slot under `.locks/<model>/`. Slots held longer than the request timeout are treated as stale (handles crashes). This prevents slow models from being hammered by concurrent requests.

`concurrency` in `models.json` sets the limit per model; `BOT_MAX_CONCURRENT` is the global fallback.
