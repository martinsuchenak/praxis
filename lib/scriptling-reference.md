# Scriptling Reference

Scriptling is Python-like. Write normal readable Python 3 style code. Use `.py` files, 4-space indentation, `True`/`False`, `None`.

## Supported

- Functions with defaults, `*args`, `**kwargs`, argument unpacking
- Lambdas, closures, recursion, `assert`, conditional expressions
- Lists, dicts, tuples, sets, slicing, `del`, chained comparisons, augmented assignment
- Classes, single inheritance, `super()`, common dunder methods
- `try`/`except`/`else`/`finally`, `with` statements, context managers
- `match`/`case` with guards and structural matching
- List/dict/set comprehensions, generator expressions
- f-strings, `.format()`, `for key, value in data.items()`
- Builtins: `len`, `str`, `int`, `float`, `bool`, `list`, `tuple`, `set`, `dict`, `range`, `enumerate`, `zip`, `map`, `filter`, `sorted`, `sum`, `min`, `max`, `isinstance`, `issubclass`

## NOT Supported

- No `async`/`await`
- No `yield`-based generators
- No type annotations
- No walrus operator (`:=`)
- No multiple inheritance
- No nested classes
- No `open()`, `eval()`, `exec()`, `globals()`, `locals()`
- Regex uses RE2 (no backreferences, no lookaround)

## Standard Libraries

| Import | Use |
|---|---|
| `json` | JSON encode/decode |
| `re` | Regular expressions (RE2) |
| `math` | Numeric functions |
| `time` | Timestamps, sleep, formatting |
| `datetime` | Dates, datetimes, timedeltas |
| `random` | Random values |
| `statistics` | Aggregations |
| `itertools` | Iteration helpers |
| `functools` | `partial`, `reduce` |
| `collections` | Containers and counters |
| `textwrap` | Text formatting |
| `hashlib` | Hashing |
| `base64` | Base64 encode/decode |
| `uuid` | UUID generation |
| `urllib.parse` | URL parsing and encoding |
| `html` | HTML escaping |
| `io` | String I/O utilities |
| `difflib` | Diffs and similarity |
| `string` | String constants and helpers |
| `platform` | Platform metadata |
| `contextlib` | Context manager helpers |

## Host-Provided Libraries (may not be available in all hosts)

| Import | Use |
|---|---|
| `requests` | HTTP client |
| `os`, `os.path`, `pathlib`, `glob` | Filesystem access |
| `subprocess` | Process execution |
| `logging` | Structured logs |
| `yaml`, `toml` | Config parsing |
| `html.parser` | HTML parsing |
| `sys` | Runtime info |
| `scriptling.ai`, `scriptling.ai.agent` | AI integration |
| `scriptling.net.websocket` | WebSockets |
| `scriptling.similarity` | Similarity search |

## HTTP Pattern

```python
import json
import requests

response = requests.get("https://api.example.com/items", timeout=10, headers={"Authorization": "Bearer " + token})
response.raise_for_status()
data = response.json()
for item in data:
    print(item["name"])
```

- Default timeout: 5 seconds if none provided
- Response: `status_code`, `text`, `body`, `headers`, `url`
- `response.json()` and `response.raise_for_status()` supported
- `body` and `text` are aliases

## Tips

- Generate standard Python first, then remove unsupported features
- Use `del` for removing items, slices, keys, attributes
- For string accumulation in loops, prefer `"".join(parts)`
- Keep code synchronous, explicit, and small
- Validate with `scriptling --lint script.py`
- Use `help("module_name")` inside a script to inspect available functions
