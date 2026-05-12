# Scriptling Reference

Scriptling is Python-like. Write normal readable Python 3 style code. Use `.py` files, 4-space indentation, `True`/`False`, `None`.

## Supported

- Functions with defaults, `*args`, `**kwargs`, and argument unpacking with `*` / `**`
- Lambdas, closures, recursion, `assert`, and conditional expressions
- Lists, dicts, tuples, sets, slicing, `del`, chained comparisons, and augmented assignment
- Classes, single inheritance, `super()`, and common dunder methods
- `try` / `except` / `else` / `finally`
- `with` statements and context managers
- `match` / `case`, including guards and structural matching for dicts and sequences
- `__name__ == "__main__"` patterns
- List/dict/set comprehensions, generator expressions
- f-strings, `.format()`, `for key, value in data.items()`
- Builtins: `len`, `str`, `int`, `float`, `bool`, `list`, `tuple`, `set`, `dict`, `range`, `enumerate`, `zip`, `map`, `filter`, `sorted`, `sum`, `min`, `max`, `isinstance`, `issubclass`

## NOT Supported

- No `async` / `await`
- No `yield`-based generator functions
- No type annotations
- No walrus operator (`:=`)
- No multiple inheritance
- No nested classes
- No built-in `open()`, `eval()`, `exec()`, `globals()`, or `locals()`
- Regex uses RE2 semantics (no backreferences, no lookaround)
- Scriptling is sandboxed by design. Filesystem, subprocess, network, and similar capabilities only exist if the host registers the relevant library.
- Fatal errors and catchable exceptions are different. Use `try` / `except` for normal exceptions, but do not assume every runtime failure is catchable.

## Standard Libraries

Built-in libraries available for import without any registration.

| Import | Description |
|---|---|
| `base64` | Base64 encoding and decoding |
| `collections` | Specialized container datatypes |
| `contextlib` | Utilities for the `with` statement (`suppress`) |
| `datetime` | Date and time formatting |
| `difflib` | Sequence comparison and diff generation |
| `functools` | Higher-order functions and decorators |
| `hashlib` | Secure hash algorithms |
| `html` | HTML escaping and unescaping |
| `io` | In-memory I/O streams (StringIO) |
| `itertools` | Iterator functions |
| `json` | Parse and generate JSON data |
| `math` | Mathematical functions and constants |
| `platform` | Platform identifying data |
| `random` | Random number generation |
| `re` | Regular expression operations (RE2) |
| `statistics` | Statistical functions |
| `string` | String constants |
| `textwrap` | Text wrapping and filling |
| `time` | Time access and conversions |
| `urllib` | URL handling (use `urllib.parse` for parsing) |
| `uuid` | UUID generation |

## Extended / Host-Provided Libraries

These are powerful, but may not exist unless the embedding app enables them.

| Import | Description |
|---|---|
| `requests` | HTTP library for sending requests |
| `os`, `os.path`, `pathlib`, `glob` | Filesystem access |
| `secrets` | Cryptographically strong random numbers |
| `fs` | Binary file reading, writing, and struct packing/unpacking |
| `subprocess` | Spawn and manage subprocesses |
| `logging` | Logging functionality |
| `yaml`, `toml` | YAML and TOML parsing/generation |
| `html.parser` | HTML/XHTML parser |
| `sys` | System-specific parameters |

## Scripting Libraries

Scriptling-specific libraries that provide functionality not available in Python's standard library. They use the `scriptling.` namespace prefix.

### AI & LLM

| Import | Description |
|---|---|
| `scriptling.ai` | AI and LLM functions for OpenAI-compatible APIs |
| `scriptling.ai.agent` | Agentic AI loop with automatic tool execution |
| `scriptling.ai.agent.interact` | Interactive terminal interface for AI agents |
| `scriptling.ai.memory` | Long-term memory store for AI agents |

### MCP Protocol

| Import | Description |
|---|---|
| `scriptling.mcp` | MCP (Model Context Protocol) client for connecting to MCP servers |
| `scriptling.mcp.tool` | Helper library for authoring MCP tools |

### Messaging

| Import | Description |
|---|---|
| `scriptling.messaging.telegram` | Telegram Bot API client |
| `scriptling.messaging.discord` | Discord Bot API client |
| `scriptling.messaging.slack` | Slack Bot API client |
| `scriptling.messaging.console` | Console-based messaging client |

### Runtime

| Import | Description |
|---|---|
| `scriptling.runtime` | Background tasks and async execution |
| `scriptling.runtime.http` | HTTP route registration and response helpers |
| `scriptling.runtime.kv` | Thread-safe key-value store |
| `scriptling.runtime.sync` | Named cross-environment concurrency primitives |
| `scriptling.runtime.sandbox` | Isolated script execution environments |

### Networking

| Import | Description |
|---|---|
| `scriptling.net.gossip` | Gossip protocol cluster membership and messaging |
| `scriptling.net.multicast` | UDP multicast group messaging |
| `scriptling.net.unicast` | UDP and TCP point-to-point messaging |
| `scriptling.net.websocket` | WebSocket client for connecting to WebSocket servers |

### Utilities

| Import | Description |
|---|---|
| `scriptling.console` | Console input/output functions |
| `scriptling.container` | Container lifecycle management for Docker, Podman, and Apple Containers |
| `scriptling.grep` | Fast file content search with regex or literal patterns |
| `scriptling.sed` | In-place file content replacement with literal strings or regex patterns |
| `scriptling.secret` | Resolve secrets through host-configured provider aliases |
| `scriptling.wait_for` | Wait for resources to become available |
| `scriptling.toon` | TOON (Token-Oriented Object Notation) encoding/decoding |
| `scriptling.similarity` | Text similarity utilities including fuzzy search and MinHash |

> Do not assume every `scriptling.*` library is available in every host. Use `help("scriptling.X")` inside a script to inspect available functions.

## Common Exceptions

These are the most useful exception types to generate in normal scripts:

- `IndexError` for out-of-range sequence access
- `KeyError` for missing dictionary keys
- `AttributeError` for missing attributes
- `ValueError` for bad values
- `TypeError` for invalid argument or operand types

Use normal `try` / `except` patterns:

```python
try:
    value = data["name"]
except KeyError:
    value = "unknown"
```

## Deletion

Scriptling supports Python-style `del` in the common cases:

```python
del items[2]
del items[1:5:2]
del data["name"]
del user.email
```

Prefer `del` over manual reassignment when the goal is to remove an item, slice, key, or attribute.

## HTTP & JSON

When generating API code, prefer this pattern:

```python
import json
import requests

response = requests.get(
    "https://api.example.com/items",
    timeout=10,
    headers={"Authorization": "Bearer " + token},
)

response.raise_for_status()
data = response.json()

for item in data:
    print(item["name"])
```

Supported methods: `requests.get()`, `requests.post()`, `requests.put()`, `requests.delete()`, `requests.patch()`.

With options:
- `timeout=N` - Explicit timeout (default: 5 seconds if none provided)
- `headers={...}` - Request headers
- `params={...}` - Query parameters
- `auth=(user, pass)` - Basic authentication
- `json=obj` - Send JSON body
- `data=body` - Send raw body

Response attributes: `status_code`, `text`, `body`, `headers`, `url`.

`response.json()` and `response.raise_for_status()` are supported. `body` and `text` are aliases.

## Common Patterns

### Retry Pattern

```python
import requests
import time

def fetch_with_retry(url, max_retries=3):
    for i in range(max_retries):
        response = requests.get(url, timeout=5)
        if response.status_code == 200:
            return response.body
        time.sleep(1)
    return None
```

### Data Processing Pipeline

```python
import json
import requests

# Fetch
response = requests.get("https://api.example.com/items", timeout=10)

# Parse
data = json.loads(response.body)

# Filter
filtered = [item for item in data if item["active"]]

# Transform
result = [{"id": x["id"], "name": x["name"].upper()} for x in filtered]

# Output
print(json.dumps(result))
```

### Batch Processing

```python
import itertools
import json
import requests

def process_batch(items):
    return [{"processed": True, "item": x} for x in items]

# Fetch items
response = requests.get("https://api.example.com/items", timeout=10)
items = json.loads(response.body)

# Process in batches of 100
for batch in itertools.batched(items, 100):
    results = process_batch(batch)
    print("Processed", len(results), "items")
```

## Best Practices

- Generate standard Python-like code first, then remove unsupported features
- Prefer builtins and standard libraries before Scriptling-specific modules
- Use dictionary methods like `.items()` and keyword arguments naturally
- Use `del` for list indexes, list slices, dict keys, and object attributes when removing data
- For HTTP, always set an explicit timeout and check or raise on status
- For JSON APIs, prefer `response.json()` or `json.loads(response.body)`
- For string accumulation in loops, prefer `"".join(parts)` over repeated concatenation
- Keep code synchronous, explicit, and small rather than clever

## Safe Default Template

Use this shape when generating a general-purpose Scriptling script:

```python
import json
import requests

def main():
    response = requests.get(
        "https://api.example.com/items",
        timeout=10,
    )
    response.raise_for_status()

    data = response.json()

    for item in data:
        print(item["name"])

if __name__ == "__main__":
    main()
```

If the script uses host-provided libraries, keep imports explicit and write the code so missing libraries fail clearly.

## Validation

Validate generated scripts with the Scriptling linter when possible:

```
scriptling --lint script.py
```

Use linting as a fast syntax and feature check before running generated code.

## Built-In Help

Scriptling provides an internal help system that can inspect builtins, libraries, and functions from inside a script:

```python
import scriptling.mcp

help(scriptling.mcp)
help("builtins")
help("scriptling.mcp")
```

Use `help()` when the host may expose additional libraries or functions beyond the common cases listed on this page.
