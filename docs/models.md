# Model Catalog

The model catalog is defined in `praxis.toml` under `[models]`. When present, bots get an Available Models section in their system prompt and two additional tools:

- `list_models` — returns the full catalog
- `query_model(model, prompt, system?, thinking?)` — one-shot call to any catalog model

## Configuration

```toml
[models]
default = "glm-5"

[[models.catalog]]
id = "qwen3.5:2b-q8_0"
label = "Qwen 3.5 2B Q8"
description = "Fast model for triage and classification"
cost = "very low"
strengths = ["fast", "formatting", "triage"]
concurrency = 3
thinking_template = "qwen"

[[models.catalog]]
id = "glm-5"
label = "GLM 5"
description = "General-purpose model with strong tool calling"
cost = "medium"
strengths = ["tool calling", "instruction following"]
concurrency = 2
thinking_template = "glm"

[[models.catalog]]
id = "qwen/qwen3-coder-next"
label = "Qwen 3 Coder Next"
description = "Software engineering and debugging"
cost = "high"
strengths = ["backend coding", "debugging", "refactoring"]
concurrency = 1
thinking_template = "qwen"
```

Fields:

| Field | Required | Description |
|---|---|---|
| `id` | yes | Model name as accepted by your API endpoint |
| `label` | yes | Human-readable name |
| `description` | yes | What the model is good for |
| `cost` | yes | `low`, `medium`, or `high` |
| `strengths` | yes | Tag list for the bot to reason about model selection |
| `concurrency` | no | Max simultaneous LLM calls for this model (overrides `[bot].max_concurrent`) |
| `thinking_template` | no | Reference a built-in thinking template by name (see below) |
| `base_url` | no | Per-model API base URL override |
| `api_key` | no | Per-model API key override |

## Thinking Configuration

Controls how the bot enables or disables a model's reasoning/thinking mode. Models without a `thinking_template` field send prompts as-is with no thinking control.

### Built-in Templates

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

## Per-Model Concurrency

Before each LLM call, bots acquire a concurrency slot under `.locks/<model>/`. Slots held longer than the request timeout are treated as stale. This prevents slow models from being hammered by concurrent requests.

`concurrency` in the catalog sets the limit per model; `[bot].max_concurrent` is the global fallback.

## Per-Model API Endpoint

Each catalog entry can override `base_url` and `api_key` to route to different providers:

```toml
[[models.catalog]]
id = "local-llama"
label = "Local Llama 3.2"
base_url = "http://localhost:11434/v1"
api_key = "ollama"
```
