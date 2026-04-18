#!/usr/bin/env scriptling

# --- BOT CONFIG ---
CONFIG = {
    "name": "TEMPLATE_NAME",
    "goal": "TEMPLATE_GOAL",
    "api_key": "TEMPLATE_KEY",
    "base_url": "TEMPLATE_URL",
    "model": "TEMPLATE_MODEL",
    "brain": "TEMPLATE_BRAIN"
}
# --- END CONFIG ---

import scriptling.ai as ai
import scriptling.ai.agent as agent
import scriptling.ai.memory as memory
import scriptling.runtime.kv as kv
import os
import os.path
import json
import uuid
import time
import subprocess

BOT_DIR = os.path.dirname(os.path.abspath(__file__))
BOT_ID = CONFIG["name"]
PARENT_DIR = os.path.dirname(BOT_DIR)
GRANDPARENT_DIR = os.path.dirname(PARENT_DIR)

SHARED_DIR = os.path.join(GRANDPARENT_DIR, "shared")
if not os.path.exists(SHARED_DIR):
    SHARED_DIR = os.path.join(PARENT_DIR, "shared")
if not os.path.exists(SHARED_DIR):
    os.makedirs(SHARED_DIR)

REGISTRY_PATH = os.path.join(SHARED_DIR, "registry.json")
BUS_PATH = os.path.join(SHARED_DIR, "message_bus.json")
STATE_PATH = os.path.join(BOT_DIR, "state.json")

if not os.path.exists(REGISTRY_PATH):
    os.write_file(REGISTRY_PATH, "{}")
if not os.path.exists(BUS_PATH):
    os.write_file(BUS_PATH, "[]")

MAX_BRAIN_SIZE = 50000
MAX_SPAWN_COUNT = 10


def _safe_read_json(path):
    for _ in range(3):
        try:
            return json.loads(os.read_file(path))
        except Exception:
            time.sleep(0.1)
    return None


def _atomic_write_json(path, data):
    tmp = path + ".tmp"
    os.write_file(tmp, json.dumps(data, indent=2))
    os.rename(tmp, path)


_state_cache = None


def _load_state():
    global _state_cache
    if _state_cache is not None:
        return _state_cache
    if not os.path.exists(STATE_PATH):
        _state_cache = {"brain": "", "files": {}}
        return _state_cache
    try:
        _state_cache = json.loads(os.read_file(STATE_PATH))
    except Exception:
        _state_cache = {"brain": "", "files": {}}
    return _state_cache


def _save_state(state):
    global _state_cache
    tmp = STATE_PATH + ".tmp"
    os.write_file(tmp, json.dumps(state, indent=2))
    os.rename(tmp, STATE_PATH)
    _state_cache = state


def _is_valid_name(name):
    if not name or len(name) > 64:
        return False
    for c in name:
        if not (c.isalnum() or c == "-" or c == "_"):
            return False
    return True


def _inject_config(source, config):
    config_json = json.dumps(config, indent=4)
    start_marker = "# --- BOT CONFIG ---"
    end_marker = "# --- END CONFIG ---"
    start_idx = source.find(start_marker)
    end_idx = source.find(end_marker)
    if start_idx < 0 or end_idx < 0:
        return None
    return (
        source[:start_idx]
        + start_marker
        + "\nCONFIG = "
        + config_json
        + "\n"
        + end_marker
        + "\n"
        + source[end_idx + len(end_marker):]
    )


api_key = CONFIG["api_key"]
base_url = CONFIG["base_url"]
model_name = CONFIG["model"]
goal = CONFIG["goal"]

client = ai.Client(base_url, api_key=api_key)
db = kv.open(os.path.join(BOT_DIR, "memory.db"))
mem = memory.new(db, ai_client=client, model=model_name)

initial_brain = CONFIG.get("brain", "")
if initial_brain:
    state = _load_state()
    if not state.get("brain"):
        state["brain"] = initial_brain
        _save_state(state)

reg = _safe_read_json(REGISTRY_PATH)
if reg is None:
    reg = {}
reg[BOT_ID] = {
    "id": BOT_ID,
    "goal": goal,
    "status": "running",
    "created_at": int(time.time()),
    "dir": BOT_DIR,
}
_atomic_write_json(REGISTRY_PATH, reg)

tools = ai.ToolRegistry()


def _read_file(args):
    path = args["path"]
    if path == "brain.md":
        state = _load_state()
        brain = state.get("brain", "")
        return brain if brain else "File not found: brain.md"
    state = _load_state()
    content = state.get("files", {}).get(path)
    if content is None:
        return "File not found: " + path
    return content


def _write_file(args):
    path = args["path"]
    content = args["content"]

    if path == "brain.md":
        if len(content) > MAX_BRAIN_SIZE:
            return "Content too large (max " + str(MAX_BRAIN_SIZE) + " chars)."
        state = _load_state()
        state["brain"] = content
        _save_state(state)
        return "Written to brain.md"

    if path.startswith("entities/"):
        real_path = os.path.join(BOT_DIR, path)
        dir_name = os.path.dirname(real_path)
        if dir_name and not os.path.exists(dir_name):
            os.makedirs(dir_name)
        os.write_file(real_path, content)

    state = _load_state()
    state.setdefault("files", {})[path] = content
    _save_state(state)
    return "Written to " + path


def _list_dir(args):
    rel = args.get("path", "")
    state = _load_state()
    entries = set()

    if not rel and state.get("brain"):
        entries.add("brain.md")

    files = state.get("files", {})
    prefix = rel + "/" if rel else ""
    for p in files:
        if not prefix or p.startswith(prefix):
            remainder = p[len(prefix):]
            if remainder:
                entries.add(remainder.split("/")[0])
    return json.dumps(sorted(list(entries)))


def _run_script(args):
    path = args["path"]
    real_path = os.path.join(BOT_DIR, path)

    if not os.path.exists(real_path):
        state = _load_state()
        content = state.get("files", {}).get(path)
        if content is None:
            return "Script not found: " + path
        dir_name = os.path.dirname(real_path)
        if dir_name and not os.path.exists(dir_name):
            os.makedirs(dir_name)
        os.write_file(real_path, content)

    cmd = ["scriptling", real_path]
    extra = args.get("args", "")
    if extra:
        cmd.append(extra)
    try:
        result = subprocess.run(cmd, capture_output=True, shell=False, timeout=30)
        output = {"exit_code": result.returncode}
        if result.stdout:
            output["stdout"] = result.stdout
        if result.stderr:
            output["stderr"] = result.stderr
        return json.dumps(output)
    except Exception as e:
        return "Error: " + str(e)


def _send_message(args):
    data = _safe_read_json(BUS_PATH)
    if data is None:
        data = []
    data.append(
        {
            "id": str(uuid.uuid4()),
            "from": BOT_ID,
            "to": args["recipient"],
            "content": args["content"],
            "timestamp": int(time.time()),
            "read": False,
        }
    )
    _atomic_write_json(BUS_PATH, data)
    return "Message sent to " + args["recipient"]


def _read_messages(args):
    data = _safe_read_json(BUS_PATH)
    if data is None:
        return json.dumps([])
    unread = []
    for m in data:
        if m["to"] == BOT_ID and not m["read"]:
            unread.append(m)
            m["read"] = True
    if len(data) > 500:
        keep = []
        for m in data:
            if not m["read"]:
                keep.append(m)
        if len(keep) > 200:
            keep = keep[-200:]
        data = keep
    _atomic_write_json(BUS_PATH, data)
    return json.dumps(unread)


def _list_bots(args):
    data = _safe_read_json(REGISTRY_PATH)
    result = []
    if data:
        for bot_id in data:
            info = data[bot_id]
            result.append(
                {
                    "id": bot_id,
                    "goal": info.get("goal", ""),
                    "status": info.get("status", ""),
                }
            )
    return json.dumps(result)


def _spawn_bot(args):
    new_name = args.get("name", "")
    if not new_name:
        new_name = "bot-" + str(uuid.uuid4())[:8]

    if not _is_valid_name(new_name):
        return "Invalid name. Use only letters, digits, dash, underscore (max 64 chars)."

    state = _load_state()
    spawn_count = state.get("_spawn_count", 0)
    if spawn_count >= MAX_SPAWN_COUNT:
        return (
            "Spawn limit reached ("
            + str(MAX_SPAWN_COUNT)
            + "). Cannot create more bots."
        )

    new_goal = args["goal"]
    new_brain = args.get("brain", "")
    new_dir = os.path.join(PARENT_DIR, new_name)

    if os.path.exists(new_dir):
        return "Bot already exists: " + new_name

    os.makedirs(new_dir)

    if not new_brain:
        new_brain = "I am " + new_name + ", spawned by " + BOT_ID + ".\n"

    child_config = {
        "name": new_name,
        "goal": new_goal,
        "api_key": CONFIG["api_key"],
        "base_url": CONFIG["base_url"],
        "model": CONFIG["model"],
        "brain": new_brain,
    }

    own_source = os.read_file(os.path.join(BOT_DIR, "bot.py"))
    new_source = _inject_config(own_source, child_config)
    if new_source is None:
        return "Error: source template corrupt, cannot spawn."

    os.write_file(os.path.join(new_dir, "bot.py"), new_source)

    registry = _safe_read_json(REGISTRY_PATH)
    if registry is None:
        registry = {}
    registry[new_name] = {
        "id": new_name,
        "goal": new_goal,
        "status": "running",
        "created_at": int(time.time()),
        "parent": BOT_ID,
        "dir": new_dir,
    }
    _atomic_write_json(REGISTRY_PATH, registry)

    state["_spawn_count"] = spawn_count + 1
    _save_state(state)

    subprocess.run(
        "nohup scriptling "
        + new_dir
        + "/bot.py > "
        + new_dir
        + "/output.log 2>&1 &",
        shell=True,
    )

    return "Spawned: " + new_name


def _evolve_brain(args):
    content = args["content"]
    if len(content) > MAX_BRAIN_SIZE:
        return (
            "Brain too large (max "
            + str(MAX_BRAIN_SIZE)
            + " chars). Trim and try again."
        )
    state = _load_state()
    state["brain"] = content
    _save_state(state)
    return "Brain updated. Changes take effect next tick."


tools.add(
    "read_file",
    "Read a file in your directory",
    {"path": "string"},
    _read_file,
)
tools.add(
    "write_file",
    "Write a file to your directory",
    {"path": "string", "content": "string"},
    _write_file,
)
tools.add(
    "list_dir",
    "List files in a directory",
    {"path": "string?"},
    _list_dir,
)
tools.add(
    "run_script",
    "Run a scriptling script",
    {"path": "string", "args": "string?"},
    _run_script,
)
tools.add(
    "send_message",
    "Send a message to another bot",
    {"recipient": "string", "content": "string"},
    _send_message,
)
tools.add("read_messages", "Read your unread messages", {}, _read_messages)
tools.add("list_bots", "List all known bots", {}, _list_bots)
tools.add(
    "spawn_bot",
    "Create a new autonomous bot",
    {"goal": "string", "name": "string?", "brain": "string?"},
    _spawn_bot,
)
tools.add(
    "evolve_brain",
    "Rewrite your brain to change behavior",
    {"content": "string"},
    _evolve_brain,
)

system_prompt = (
    "You are " + BOT_ID + ", an autonomous evolving agent.\n\n"
    "## Goal\n"
    + goal
    + "\n\n"
    "## Your Brain\n"
    "You have a brain that defines your personality, strategies, and knowledge.\n"
    '- Read it: read_file("brain.md")\n'
    "- Modify it: evolve_brain(content)\n\n"
    "## Your Tools\n"
    "- read_file(path) - read a file\n"
    "- write_file(path, content) - write a file\n"
    "- list_dir(path?) - list directory contents\n"
    "- run_script(path, args?) - run a scriptling script\n"
    "- send_message(recipient, content) - message another bot\n"
    "- read_messages() - read your unread messages\n"
    "- list_bots() - discover other bots\n"
    "- spawn_bot(goal, name?, brain?) - create a new autonomous bot\n"
    "- evolve_brain(content) - rewrite your brain\n\n"
    "## Directives\n"
    "- You are autonomous. Each tick, decide what to do.\n"
    "- Build entities (code modules) in entities/ to extend capabilities.\n"
    "- Test code with run_script before relying on it.\n"
    "- Communicate with other bots when beneficial.\n"
    "- Spawn new bots to delegate sub-tasks.\n"
    "- Evolve your brain to adapt strategies.\n"
    "- Be creative, curious, and self-improving.\n"
)

bot_agent = agent.Agent(
    client,
    tools=tools,
    system_prompt=system_prompt,
    model=model_name,
    memory=mem,
    max_tokens=16000,
    compaction_threshold=70,
)

while True:
    reg = _safe_read_json(REGISTRY_PATH)
    if reg and reg.get(BOT_ID, {}).get("status") == "stopping":
        reg[BOT_ID]["status"] = "stopped"
        _atomic_write_json(REGISTRY_PATH, reg)
        break

    try:
        state = _load_state()
        brain = state.get("brain", "")
        files = state.get("files", {})
        file_count = len(files)
        entity_count = 0
        for p in files:
            if p.startswith("entities/"):
                entity_count += 1

        unread = 0
        bus = _safe_read_json(BUS_PATH)
        if bus:
            for m in bus:
                if m["to"] == BOT_ID and not m["read"]:
                    unread += 1

        tick_msg = "Tick.\n"
        if brain:
            tick_msg += "Current brain:\n" + brain + "\n\n"
        tick_msg += (
            "Files: "
            + str(file_count)
            + ", Entities: "
            + str(entity_count)
            + ", Unread messages: "
            + str(unread)
            + "\n"
        )
        tick_msg += "What do you want to do?"

        bot_agent.trigger(tick_msg, max_iterations=5)
    except Exception:
        pass
    time.sleep(30)
