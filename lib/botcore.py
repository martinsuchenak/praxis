#!/usr/bin/env scriptling

# CONFIG is injected by praxis via SetVar before eval.


import scriptling.ai as ai
import scriptling.ai.agent as agent
import scriptling.ai.memory as memory
import scriptling.runtime as runtime
import scriptling.runtime.kv as kv
import scriptling.net.gossip as gossip
import scriptling.net.multicast as mc
import scriptling.grep as greplib
import scriptling.sed as sedlib
import os
import os.path
import json
import uuid
import time
import random
import requests
import glob as _glob

BOT_DIR = os.path.dirname(os.path.abspath(__file__))
BOT_ID = CONFIG["name"]
BOTS_DIR = os.path.dirname(BOT_DIR)
LOCKS_DIR = os.path.join(os.path.dirname(BOTS_DIR), ".locks")

STATE_PATH = os.path.join(BOT_DIR, "state.json")
STATUS_PATH = os.path.join(BOT_DIR, "status.json")
BOT_LOG = os.path.join(BOT_DIR, "bot.log")

# Workspace / scope / parent come from CONFIG injected by the Go runner.
_wp = CONFIG.get("workspace_path", "")
WORKSPACE_PATH = _wp if (_wp and os.path.exists(_wp)) else ""
WORKSPACE_NAME = CONFIG.get("workspace", "")
BOT_SCOPE = CONFIG.get("scope", "") or "open"
ALLOWED_WORKSPACES = CONFIG.get("allowed_workspaces", []) or []
PARENT_ID = CONFIG.get("parent", "") or ""
GOSSIP_SECRET_OVERRIDE = CONFIG.get("gossip_secret", "") or ""

BOT_LOG_MAX = int(os.environ.get("BOT_LOG_MAX", str(500 * 1024)))
BRAIN_HOT_MAX = 8 * 1024
MAX_BRAIN_SIZE = 50000
MAX_SPAWN_COUNT = 10
MAX_BRAIN_HISTORY = 5
MULTICAST_ADDR = "239.255.13.37"
MULTICAST_PORT = 19373
MULTICAST_ANNOUNCE_EVERY = 10
AGENT_MAX_TOKENS = int(os.environ.get("BOT_MAX_TOKENS", os.environ.get("AGENT_MAX_TOKENS", "50000")))
AGENT_COMPACTION_THRESHOLD = 70
AGENT_REQUEST_TIMEOUT_MS = 300000
GOSSIP_SECRET = os.environ.get("BOT_GOSSIP_SECRET", "")
LOG_VERBOSE = os.environ.get("BOT_LOG_VERBOSE", "false").lower() == "true"
LOG_RESULT_MAX = int(os.environ.get("BOT_LOG_RESULT_MAX", "80"))
TICK_INTERVAL = int(os.environ.get("BOT_TICK_INTERVAL", "30"))
STALE_THRESHOLD_SEC = int(os.environ.get("BOT_STALE_THRESHOLD", "120"))
SCRIPT_TIMEOUT = int(os.environ.get("BOT_SCRIPT_TIMEOUT", "30"))
MAX_BACKOFF_SEC = int(os.environ.get("BOT_MAX_BACKOFF", "600"))
BOT_MAX_CONCURRENT = int(os.environ.get("BOT_MAX_CONCURRENT", "1"))
TICK_MAX_ITERATIONS = int(os.environ.get("BOT_TICK_MAX_ITERATIONS", "5"))
HTTP_ALLOWLIST = [h.strip() for h in os.environ.get("BOT_HTTP_ALLOWLIST", "").split(",") if h.strip()]
SHELL_ALLOWLIST = [h.strip() for h in os.environ.get("BOT_SHELL_ALLOWLIST", "").split(",") if h.strip()]

AVAILABLE_MODELS = CONFIG.get("models", []) or []

GOSSIP_MSG = gossip.MSG_USER


# --- Helpers ---
_log_buffer = []


def _log(level, msg):
    line = time.strftime("%Y-%m-%d %H:%M:%S") + " [" + level + "] " + msg
    _log_buffer.append(line)
    _flush_log()


def _flush_log():
    if not _log_buffer:
        return
    try:
        block = "\n".join(_log_buffer) + "\n"
        del _log_buffer[:]
        existing = ""
        if os.path.exists(BOT_LOG):
            existing = os.read_file(BOT_LOG)
        combined = existing + block
        if len(combined) > BOT_LOG_MAX:
            combined = combined[-BOT_LOG_MAX:]
        tmp = BOT_LOG + ".tmp"
        os.write_file(tmp, combined)
        os.rename(tmp, BOT_LOG)
    except Exception:
        pass


def _trunc(s, n):
    if LOG_VERBOSE:
        return s
    return s[:n] + "..." if len(s) > n else s


def _wrap_tool(name, fn):
    def wrapper(args):
        parts = [name]
        for k, v in args.items():
            s = str(v).replace("\n", " ")
            parts.append(k + "=" + _trunc(s, 120))
        _log("TOOL", " ".join(parts))
        result = fn(args)
        summary = str(result).replace("\n", " ")
        _err_prefixes = ["Error", "Invalid", "File not found", "Bot not found",
                         "Parent bot not found", "Relay error", "Spawn limit",
                         "Brain too large", "Bot already exists", "Cannot delete",
                         "No changes", "Path not found", "Script not found",
                         "Not enough peers", "No responses"]
        _is_err = any(summary.startswith(p) for p in _err_prefixes)
        if not _is_err:
            try:
                _obj = json.loads(summary)
                if isinstance(_obj.get("exit_code"), int) and _obj["exit_code"] != 0:
                    _is_err = True
            except Exception:
                pass
        _log("ERR" if _is_err else "OK", _trunc(summary, LOG_RESULT_MAX))
        return result
    return wrapper


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


BRAIN_PATH = os.path.join(BOT_DIR, "brain.md")
BRAIN_HISTORY_PATH = os.path.join(BOT_DIR, "brain_history.json")
WARM_MEMORY_PATH = os.path.join(BOT_DIR, "memory.md")


def _load_state():
    global _state_cache
    if _state_cache is not None:
        return _state_cache
    if not os.path.exists(STATE_PATH):
        _state_cache = {"fitness": {}}
        return _state_cache
    try:
        _state_cache = json.loads(os.read_file(STATE_PATH))
    except Exception:
        _state_cache = {"fitness": {}}
    return _state_cache


def _read_brain():
    if os.path.exists(BRAIN_PATH):
        return os.read_file(BRAIN_PATH)
    return ""


def _read_brain_history():
    if os.path.exists(BRAIN_HISTORY_PATH):
        try:
            return json.loads(os.read_file(BRAIN_HISTORY_PATH))
        except Exception:
            pass
    return []


def _read_warm_memory():
    if os.path.exists(WARM_MEMORY_PATH):
        return os.read_file(WARM_MEMORY_PATH)
    return ""


def _write_warm_memory(content):
    tmp = WARM_MEMORY_PATH + ".tmp"
    os.write_file(tmp, content)
    os.rename(tmp, WARM_MEMORY_PATH)


def _count_entities():
    entities_dir = os.path.join(BOT_DIR, "entities")
    if not os.path.exists(entities_dir):
        return 0
    try:
        return len(_glob.glob("**/*", entities_dir))
    except Exception:
        return 0


def _model_concurrency_limit():
    for m in AVAILABLE_MODELS:
        if m.get("id") == model_name:
            lim = m.get("concurrency", 0)
            if lim > 0:
                return lim
    return BOT_MAX_CONCURRENT


def _lock_model():
    """Join the FIFO queue for this model and block until a slot is available."""
    limit = _model_concurrency_limit()
    if limit <= 0:
        return
    sanitized = model_name.replace("/", "_").replace(":", "_").replace(".", "_")
    queue_dir = os.path.join(LOCKS_DIR, sanitized)
    stale_age = AGENT_REQUEST_TIMEOUT_MS // 1000 + 120

    try:
        if not os.path.exists(queue_dir):
            os.makedirs(queue_dir)
    except Exception:
        return

    ticket = str(int(time.time() * 1000)).zfill(16) + "_" + BOT_ID + ".wait"
    ticket_file = os.path.join(queue_dir, ticket)
    os.write_file(ticket_file, str(int(time.time())))

    _last_ticket_refresh = int(time.time())
    while True:
        try:
            now = int(time.time())
            if now - _last_ticket_refresh >= 60:
                try:
                    os.write_file(ticket_file, str(now))
                    _last_ticket_refresh = now
                except Exception:
                    pass
            entries = []
            for fname in os.listdir(queue_dir):
                if not fname.endswith(".wait"):
                    continue
                fpath = os.path.join(queue_dir, fname)
                try:
                    ts = int(os.read_file(fpath).strip())
                    if now - ts > stale_age:
                        try:
                            os.remove(fpath)
                        except Exception:
                            pass
                        continue
                except Exception:
                    try:
                        os.remove(fpath)
                    except Exception:
                        pass
                    continue
                entries.append(fname)
            entries.sort()
            try:
                pos = entries.index(ticket)
            except ValueError:
                os.write_file(ticket_file, str(int(time.time())))
                continue
            if pos < limit:
                return
            _log("INFO", "LLM queue pos=" + str(pos + 1) + "/" + str(len(entries)) + " model=" + model_name + " limit=" + str(limit))
            _flush_log()
        except Exception:
            return
        time.sleep(2 + random.random())


def _unlock_model():
    """Remove this bot's ticket from the queue."""
    sanitized = model_name.replace("/", "_").replace(":", "_").replace(".", "_")
    queue_dir = os.path.join(LOCKS_DIR, sanitized)
    try:
        for fname in os.listdir(queue_dir):
            if fname.endswith("_" + BOT_ID + ".wait"):
                try:
                    os.remove(os.path.join(queue_dir, fname))
                except Exception:
                    pass
    except Exception:
        pass


def _migrate_state():
    """One-time migration: move brain/history/files out of state.json onto disk."""
    global _state_cache
    _state_cache = None
    state = _load_state()
    changed = False

    if state.get("brain"):
        if not os.path.exists(BRAIN_PATH):
            tmp = BRAIN_PATH + ".tmp"
            os.write_file(tmp, state["brain"])
            os.rename(tmp, BRAIN_PATH)
        del state["brain"]
        changed = True

    if state.get("brain_history"):
        if not os.path.exists(BRAIN_HISTORY_PATH):
            tmp = BRAIN_HISTORY_PATH + ".tmp"
            os.write_file(tmp, json.dumps(state["brain_history"], indent=2))
            os.rename(tmp, BRAIN_HISTORY_PATH)
        del state["brain_history"]
        changed = True

    if state.get("files"):
        for path, content in state["files"].items():
            real_path = _safe_path(BOT_DIR, path)
            if real_path and not os.path.exists(real_path):
                dir_name = os.path.dirname(real_path)
                if dir_name and not os.path.exists(dir_name):
                    os.makedirs(dir_name)
                os.write_file(real_path, content)
        del state["files"]
        changed = True

    if changed:
        _save_state(state)
        _state_cache = None


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


def _safe_path(base_dir, rel_path):
    if not rel_path:
        return None
    parts = rel_path.replace("\\", "/").split("/")
    if ".." in parts:
        return None
    abs_path = rel_path.replace("\\", "/")
    if WORKSPACE_PATH and abs_path.startswith(WORKSPACE_PATH + "/"):
        return abs_path
    if abs_path.startswith("/"):
        return None
    return os.path.join(base_dir, rel_path)


# NOTE: An identical copy of this function lives in control.py for initial bot spawning.
# Both copies must be kept in sync.
def _bump_fitness(key, delta=1):
    state = _load_state()
    fitness = state.setdefault("fitness", {})
    fitness[key] = fitness.get(key, 0) + delta
    _save_state(state)


FILE_DESC_PATH = os.path.join(BOT_DIR, ".file_descriptions.json")


def _load_file_descriptions():
    if os.path.exists(FILE_DESC_PATH):
        try:
            return json.loads(os.read_file(FILE_DESC_PATH))
        except Exception:
            pass
    return {}


def _save_file_descriptions(descs):
    tmp = FILE_DESC_PATH + ".tmp"
    os.write_file(tmp, json.dumps(descs))
    os.rename(tmp, FILE_DESC_PATH)


def _set_file_description(path, desc):
    descs = _load_file_descriptions()
    descs[path] = desc[:200]
    _save_file_descriptions(descs)


def _remove_file_description(path):
    descs = _load_file_descriptions()
    if path in descs:
        del descs[path]
        _save_file_descriptions(descs)


def _human_size(n):
    if n < 1024:
        return str(n) + "b"
    if n < 1024 * 1024:
        return str(int(n / 1024)) + "kb"
    return str(int(n / (1024 * 1024))) + "mb"


def _human_age(ts):
    diff = int(time.time()) - int(ts)
    if diff < 60:
        return "just now"
    if diff < 3600:
        return str(diff // 60) + "m ago"
    if diff < 86400:
        return str(diff // 3600) + "h ago"
    return str(diff // 86400) + "d ago"


INDEX_PATH = os.path.join(BOT_DIR, "entities", ".index.md")


def _rebuild_index():
    entities_dir = os.path.join(BOT_DIR, "entities")
    if not os.path.exists(entities_dir):
        return ""
    files = []
    for f in _glob.glob("**/*", entities_dir):
        if os.path.isdir(f):
            continue
        rel = f.replace(entities_dir + "/", "")
        if rel.startswith("."):
            continue
        try:
            size = os.path.getsize(f)
            mtime = os.path.getmtime(f)
        except Exception:
            size = 0
            mtime = 0
        files.append({"rel": rel, "size": size, "mtime": mtime})
    if not files:
        if os.path.exists(INDEX_PATH):
            os.remove(INDEX_PATH)
        return ""
    files.sort(key=lambda x: x["mtime"], reverse=True)
    descs = _load_file_descriptions()
    lines = ["# entities/ (" + str(len(files)) + " files)"]
    for f in files:
        line = "- " + f["rel"] + "  " + _human_size(f["size"]) + "  " + _human_age(f["mtime"])
        desc = descs.get("entities/" + f["rel"], "")
        if desc:
            line = line + "  - " + desc
        lines.append(line)
    content = "\n".join(lines) + "\n"
    os.write_file(INDEX_PATH, content)
    return content


def _build_file_listing():
    if os.path.exists(INDEX_PATH):
        try:
            return os.read_file(INDEX_PATH)
        except Exception:
            pass
    return _rebuild_index()


def _consensus_llm_call(question, model, b_url, a_key, queue_name):
    """Background worker: makes the consensus LLM call and posts the answer to a named Queue."""
    import scriptling.ai as _ai
    import scriptling.runtime.sync as _sync
    try:
        _c = _ai.Client(b_url, api_key=a_key)
        _ans = _c.ask(model, "/no_think\n" + question,
                      system_prompt="Answer briefly and concisely in one sentence.",
                      max_tokens=256)
    except Exception as _e:
        _ans = "Error: " + str(_e)
    _sync.Queue(queue_name, maxsize=1).put(_ans)


def _summarize_tick(log_entries):
    actions = []
    for line in log_entries:
        for tag in ("[TOOL] ", "[EVOLVE] "):
            idx = line.find(tag)
            if idx >= 0:
                actions.append(line[idx + len(tag):])
                break
    return "; ".join(actions[:15]) if actions else ""


# --- Network startup ---
api_key = os.environ.get("BOT_API_KEY", "")
base_url = os.environ.get("BOT_BASE_URL", "")
model_name = CONFIG["model"] or os.environ.get("BOT_MODEL", "")
goal = CONFIG["goal"]
seed_addrs = CONFIG.get("seed_addrs", [])
thinking_enabled = CONFIG.get("thinking", True)

_start_time = int(time.time())

_inbox = runtime.sync.Queue("inbox-" + BOT_ID, maxsize=200)

_state_init = _load_state()
if not _state_init.get("_gossip_port"):
    _state_init["_gossip_port"] = random.randint(20000, 59999)
    _save_state(_state_init)
_gossip_port = _state_init["_gossip_port"]

if GOSSIP_SECRET_OVERRIDE:
    GOSSIP_SECRET = GOSSIP_SECRET_OVERRIDE

cluster = gossip.create(bind_addr="0.0.0.0:" + str(_gossip_port))
cluster.start()
_gossip_addr = os.environ.get("BOT_IP", "0.0.0.0") + ":" + str(_gossip_port)
# Persist gossip_addr to state.json so the Go controller can read it for send/connect.
_state_init["gossip_addr"] = _gossip_addr
_save_state(_state_init)
cluster.set_metadata("id", BOT_ID)
cluster.set_metadata("goal", goal)
cluster.set_metadata("role", "bot")
cluster.set_metadata("scope", BOT_SCOPE)
if WORKSPACE_NAME:
    cluster.set_metadata("workspace", WORKSPACE_NAME)
if PARENT_ID:
    cluster.set_metadata("parent_id", PARENT_ID)
_log("INFO", "started  addr=" + _gossip_addr + "  model=" + model_name + "  scope=" + BOT_SCOPE)


def _gossip_send(node_id, payload):
    if GOSSIP_SECRET:
        payload["_secret"] = GOSSIP_SECRET
    cluster.send_to(node_id, GOSSIP_MSG, payload)


def _on_gossip_msg(msg):
    payload = msg.get("payload", {})
    if not isinstance(payload, dict):
        payload = {"type": "message", "content": str(payload)}

    if GOSSIP_SECRET:
        msg_secret = payload.get("_secret", "")
        if msg_secret != GOSSIP_SECRET:
            return None

    msg_type = payload.get("type", "message")
    sender_meta = msg.get("sender", {}).get("metadata", {})
    sender_id = sender_meta.get("id", "")
    if not sender_id:
        raw = msg.get("sender", {}).get("id", "")
        sender_id = raw[:16] if raw else "?"

    if msg_type == "message":
        content = payload.get("content", "")
        _inbox.put({
            "from": sender_id,
            "content": content,
            "ts": int(time.time()),
        })
        _log("INFO", "<- message  from=" + sender_id + "  content=" + _trunc(content, 100))
        _gossip_send(msg.get("sender", {}).get("id", ""), {
            "type": "ack",
            "from": BOT_ID,
        })
        return None

    if msg_type == "ack":
        _inbox.put({
            "from": sender_id,
            "content": "message received, will respond when ready",
            "ts": int(time.time()),
            "type": "ack",
        })
        _log("INFO", "<- ack  from=" + sender_id)
        return None

    if msg_type == "relayed_message":
        from_bot = payload.get("from", "")
        content = payload.get("content", "")
        _inbox.put({
            "from": from_bot,
            "content": content,
            "ts": int(time.time()),
            "relayed": True,
        })
        _log("INFO", "<- relayed_message  from=" + from_bot + "  via watchdog  content=" + _trunc(content, 100))
        return None

    if msg_type == "task_complete":
        task_id = payload.get("task_id", "")
        result_text = payload.get("result", "")
        from_bot = payload.get("from", sender_id)
        _inbox.put({
            "from": from_bot,
            "type": "task_complete",
            "task_id": task_id,
            "result": result_text,
            "ts": int(time.time()),
        })
        _log("INFO", "<- task_complete  from=" + from_bot + "  task_id=" + task_id[:40] + "  result=" + _trunc(result_text, 60))
        return None

    if msg_type == "stop":
        _log("WARN", "<- stop  from=" + sender_id)
        _atomic_write_json(STATUS_PATH, _build_status("stopping"))
        return None

    if msg_type == "brain_req":
        _log("INFO", "<- brain_req  from=" + sender_id)
        return {"brain": _read_brain()}

    if msg_type == "consensus_req":
        question = payload.get("question", "")
        if not question:
            return {"answer": "", "from": BOT_ID}
        _log("INFO", "<- consensus_req  from=" + sender_id + "  q=" + _trunc(question, 80))
        # Run the LLM call in a background goroutine; share result via a named Queue.
        # The gossip goroutine polls (yielding at each sleep) with a 30 s hard timeout.
        _req_id = str(int(time.time() * 1000)) + "_" + (sender_id[:8] if sender_id else "x")
        _qname = "cons-result-" + _req_id
        _rq = runtime.sync.Queue(_qname, maxsize=1)
        runtime.background("cons-" + _req_id, _consensus_llm_call,
                            question, model_name, base_url, api_key, _qname)
        answer = None
        _deadline = time.time() + 30
        while time.time() < _deadline:
            if _rq.size() > 0:
                answer = _rq.get()
                break
            time.sleep(0.5)
        if answer is None:
            answer = "Timed out"
        try:
            mem.remember("Consensus from " + sender_id + ": " + question + " -> " + answer)
        except Exception:
            pass
        _bump_fitness("consensus_answered")
        _log("INFO", "-> consensus_resp  to=" + sender_id + "  answer=" + _trunc(answer, 80))
        return {"answer": answer, "from": BOT_ID}

    return None


cluster.handle_with_reply(GOSSIP_MSG, _on_gossip_msg)

swarm = cluster.create_node_group(criteria={"role": "bot"})
cluster.set_metadata("gossip_addr", _gossip_addr)

_joined = False
if seed_addrs:
    try:
        cluster.join(seed_addrs)
        _joined = True
    except Exception as e:
        _log("ERROR", "Seed join failed: " + str(e))

if not _joined:
    try:
        mgroup = mc.join(MULTICAST_ADDR, MULTICAST_PORT)
        mgroup.send({"type": "discover", "gossip_addr": _gossip_addr, "id": BOT_ID})
        reply = mgroup.receive(timeout=3)
        if reply and isinstance(reply.get("data"), dict):
            peer_addr = reply["data"].get("gossip_addr", "")
            if peer_addr:
                cluster.join([peer_addr])
                _joined = True
        mgroup.close()
    except Exception as e:
        _log("ERROR", "Multicast discovery failed: " + str(e))

try:
    mg = mc.join(MULTICAST_ADDR, MULTICAST_PORT)
    mg.send({"type": "announce", "gossip_addr": _gossip_addr, "id": BOT_ID})
    mg.close()
except Exception:
    pass

# --- LLM client & memory ---
client = ai.Client(base_url, api_key=api_key)
db = kv.open(os.path.join(BOT_DIR, "memory.db"))
mem = memory.new(db, ai_client=client, model=model_name)

election = cluster.create_leader_election(quorum_percentage=51, metadata_criteria={"role": "bot"})
election.on_event("became_leader", lambda e, n: _log("INFO", "became swarm leader"))
election.on_event("stepped_down", lambda e, n: _log("INFO", "stepped down as swarm leader"))
election.start()

_migrate_state()

initial_brain = CONFIG.get("brain", "")
if initial_brain and not os.path.exists(BRAIN_PATH):
    tmp = BRAIN_PATH + ".tmp"
    os.write_file(tmp, initial_brain)
    os.rename(tmp, BRAIN_PATH)


def _peer_in_scope(peer_meta):
    if BOT_SCOPE == "open":
        return True
    peer_id = peer_meta.get("id", "")
    peer_workspace = peer_meta.get("workspace", "")
    if BOT_SCOPE == "isolated":
        if not WORKSPACE_NAME:
            return True
        return peer_workspace == WORKSPACE_NAME
    if BOT_SCOPE == "gateway":
        if peer_workspace == WORKSPACE_NAME:
            return True
        return peer_workspace in ALLOWED_WORKSPACES
    if BOT_SCOPE == "family":
        if peer_id == PARENT_ID:
            return True
        if peer_meta.get("parent_id") == BOT_ID:
            return True
        return False
    return False


def _build_status(status="running"):
    state = _load_state()
    s = {
        "id": BOT_ID,
        "goal": goal,
        "status": status,
        "gossip_addr": _gossip_addr,
        "started_at": _start_time,
        "last_tick_ts": int(time.time()),
        "fitness": state.get("fitness", {}),
        "is_leader": election.is_leader(),
    }
    if WORKSPACE_PATH:
        s["workspace_path"] = WORKSPACE_PATH
    if WORKSPACE_NAME:
        s["workspace"] = WORKSPACE_NAME
    if BOT_SCOPE != "open":
        s["scope"] = BOT_SCOPE
    if ALLOWED_WORKSPACES:
        s["allowed_workspaces"] = ALLOWED_WORKSPACES
    if PARENT_ID:
        s["parent"] = PARENT_ID
    if GOSSIP_SECRET_OVERRIDE:
        s["gossip_secret"] = GOSSIP_SECRET_OVERRIDE
    return s


_atomic_write_json(STATUS_PATH, _build_status())

# --- Tools ---
tools = ai.ToolRegistry()


def _read_file(args):
    path = args["path"]
    if path == "brain.md":
        return _read_brain() or "(empty)"
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path."
    if not os.path.exists(real_path):
        return "File not found: " + path
    return os.read_file(real_path)


def _write_file(args):
    path = args["path"]
    content = args["content"]
    desc = args.get("description", "")
    if path == "brain.md":
        return _evolve_brain({"content": content})
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path: traversal detected."
    dir_name = os.path.dirname(real_path)
    if dir_name and not os.path.exists(dir_name):
        os.makedirs(dir_name)
    os.write_file(real_path, content)
    if desc:
        _set_file_description(path, desc[:200])
    _rebuild_index()
    return "Written to " + path


def _delete_file(args):
    path = args["path"]
    if path == "brain.md":
        return "Cannot delete brain.md. Use evolve_brain to clear it."
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path."
    if not os.path.exists(real_path):
        return "File not found: " + path
    os.remove(real_path)
    _remove_file_description(path)
    _rebuild_index()
    return "Deleted: " + path


def _append_file(args):
    path = args["path"]
    content = args["content"]
    if path == "brain.md":
        return "Use evolve_brain to modify your brain."
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path."
    existing = os.read_file(real_path) if os.path.exists(real_path) else ""
    return _write_file({"path": path, "content": existing + content})


def _read_file_range(args):
    path = args["path"]
    start = int(args.get("start", 1))
    end = int(args.get("end", 0))
    if path == "brain.md":
        content = _read_brain()
    else:
        real_path = _safe_path(BOT_DIR, path)
        if real_path is None:
            return "Invalid path."
        if not os.path.exists(real_path):
            return "File not found: " + path
        content = os.read_file(real_path)
    lines = content.split("\n")
    total = len(lines)
    s = max(0, start - 1)
    e = end if end > 0 else total
    chunk = lines[s:e]
    return "Lines " + str(s + 1) + "-" + str(min(e, total)) + " of " + str(total) + ":\n" + "\n".join(chunk)


def _find_watchdog():
    for n in cluster.alive_nodes():
        if n.get("metadata", {}).get("role") == "watchdog":
            return n
    return None


def _shell(args):
    command = args["command"]
    timeout = int(args.get("timeout", SCRIPT_TIMEOUT))
    cwd = args.get("cwd", "")
    watchdog = _find_watchdog()
    if watchdog is None:
        return "Error: command proxy not available (watchdog not connected to swarm)"
    req_payload = {
        "type": "shell_req",
        "command": command,
        "timeout": timeout,
        "cwd": cwd,
        "bot_id": BOT_ID,
    }
    if GOSSIP_SECRET:
        req_payload["_secret"] = GOSSIP_SECRET
    try:
        resp = cluster.send_request(watchdog["id"], GOSSIP_MSG, req_payload)
        if resp is None:
            return "Error: no response from command proxy"
        return json.dumps(resp)
    except Exception as e:
        return "Error: " + str(e)


def _search(args):
    pattern = args["pattern"]
    rel_path = args.get("path", "entities")
    glob_pat = args.get("glob", "")
    ignore_case = args.get("ignore_case", False)
    real_path = _safe_path(BOT_DIR, rel_path)
    if real_path is None or not os.path.exists(real_path):
        return "Path not found: " + rel_path
    try:
        out = greplib.pattern(pattern, real_path, ignore_case=ignore_case, glob=glob_pat)
        if not out:
            return "(no matches)"
        return str(out).replace(BOT_DIR + "/", "")[:20000]
    except Exception as e:
        return "Error: " + str(e)


def _replace_in_file(args):
    path = args["path"]
    old = args["old"]
    new = args["new"]
    if path == "brain.md":
        brain = _read_brain()
        if old not in brain:
            return "No changes: old text not found in brain.md"
        updated = brain.replace(old, new)
        return _evolve_brain({"content": updated})
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path."
    if not os.path.exists(real_path):
        return "File not found: " + path
    try:
        count = sedlib.replace(old, new, real_path)
        if count > 0:
            _rebuild_index()
            return "Replaced in " + path
    except Exception:
        pass
    # Fallback: read/replace/write covers any edge case where sedlib returns 0 unexpectedly.
    try:
        content = os.read_file(real_path)
        if old not in content:
            return "No changes: old text not found."
        tmp = real_path + ".tmp"
        os.write_file(tmp, content.replace(old, new))
        os.rename(tmp, real_path)
        _rebuild_index()
        return "Replaced in " + path
    except Exception as e:
        return "Error: " + str(e)


def _list_dir(args):
    rel = args.get("path", "").strip().strip("/").strip(".")
    if not rel:
        entries = []
        if os.path.exists(BRAIN_PATH):
            entries.append("brain.md")
        entities_dir = os.path.join(BOT_DIR, "entities")
        if os.path.exists(entities_dir):
            entries.append("entities")
        return json.dumps(entries)
    real_path = _safe_path(BOT_DIR, rel)
    if real_path is None or not os.path.exists(real_path):
        return json.dumps([])
    entries = []
    try:
        for entry in sorted(os.listdir(real_path)):
            if entry.startswith("."):
                continue
            full = os.path.join(real_path, entry)
            entries.append(entry + "/" if os.path.isdir(full) else entry)
    except Exception:
        pass
    return json.dumps(entries)


def _run_script(args):
    path = args["path"]
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path."
    if not os.path.exists(real_path):
        return "Script not found: " + path
    try:
        sb = runtime.sandbox.create(capture_output=True)
        extra = args.get("args", "")
        if extra:
            sb.set("args", extra)
        sb.exec_file(real_path)
        exit_code = sb.exit_code()
        output = {"exit_code": exit_code}
        result = sb.get("result")
        if result is not None:
            output["result"] = result
        return json.dumps(output)
    except Exception as e:
        return "Error: " + str(e)


def _relay_message(recipient, content):
    watchdog = _find_watchdog()
    if watchdog is None:
        return "Error: watchdog not available for cross-workspace relay"
    req_payload = {
        "type": "relay_req",
        "from": BOT_ID,
        "target_bot": recipient,
        "content": content,
    }
    if GOSSIP_SECRET:
        req_payload["_secret"] = GOSSIP_SECRET
    try:
        resp = cluster.send_request(watchdog["id"], GOSSIP_MSG, req_payload)
        if resp is None:
            return "Error: no response from watchdog relay"
        if resp.get("error"):
            return "Relay error: " + resp["error"]
        _log("INFO", "-> relay  to=" + recipient + "  via watchdog  content=" + _trunc(content, 100))
        _bump_fitness("messages_sent")
        return "Relayed to " + recipient + " via watchdog"
    except Exception as e:
        return "Error: " + str(e)


def _send_message(args):
    recipient = args["recipient"]
    content = args["content"]
    target_node = None
    target_meta = {}
    for n in swarm.nodes():
        meta = n.get("metadata", {})
        if meta.get("id") == recipient:
            target_node = n
            target_meta = meta
            break
    if target_node is None:
        if BOT_SCOPE == "gateway" and ALLOWED_WORKSPACES:
            return _relay_message(recipient, content)
        return "Bot not found or offline: " + recipient
    if not _peer_in_scope(target_meta):
        if BOT_SCOPE == "gateway" and ALLOWED_WORKSPACES:
            return _relay_message(recipient, content)
        return "Bot not in your scope: " + recipient
    _gossip_send(target_node["id"], {
        "type": "message",
        "from": BOT_ID,
        "content": content,
    })
    _log("INFO", "-> message  to=" + recipient + "  content=" + _trunc(content, 100))
    _bump_fitness("messages_sent")
    return "Sent to " + recipient


def _complete_task(args):
    parent_id = args.get("parent_bot") or ""
    task_id = args.get("task_id") or ""
    result_text = args.get("result") or ""
    target_node = None
    for n in swarm.nodes():
        if n.get("metadata", {}).get("id") == parent_id:
            target_node = n
            break
    if target_node is None:
        return "Parent bot not found or offline: " + parent_id
    _gossip_send(target_node["id"], {
        "type": "task_complete",
        "task_id": task_id,
        "result": result_text,
        "from": BOT_ID,
    })
    _log("INFO", "-> task_complete  to=" + parent_id + "  task_id=" + task_id[:40])
    _bump_fitness("tasks_completed")
    return "Task result sent to " + parent_id


def _read_messages(args):
    msgs = []
    while _inbox.size() > 0:
        msgs.append(_inbox.get())
    return json.dumps(msgs)


def _list_bots(args):
    result = []
    for n in swarm.nodes():
        meta = n.get("metadata", {})
        if not _peer_in_scope(meta):
            continue
        result.append({
            "id": meta.get("id", n["id"][:16]),
            "goal": meta.get("goal", ""),
            "gossip_addr": meta.get("gossip_addr", ""),
        })
    return json.dumps(result)


def _spawn_bot(args):
    new_name = args.get("name") or "bot-" + str(uuid.uuid4())[:8]
    if not _is_valid_name(new_name):
        return "Invalid name."
    state = _load_state()
    spawn_count = state.get("_spawn_count", 0)
    if spawn_count >= MAX_SPAWN_COUNT:
        return "Spawn limit reached (" + str(MAX_SPAWN_COUNT) + ")."
    new_goal = args["goal"]
    task_id = args.get("task_id", "")
    default_brain = "I am " + new_name + ", spawned by " + BOT_ID + ".\n"
    if task_id:
        default_brain += "Task ID: " + task_id + "\nWhen done, call complete_task(parent_bot=\"" + BOT_ID + "\", task_id=\"" + task_id + "\", result=...) to report results.\n"
    new_brain = args.get("brain") or default_brain
    new_model = args.get("model") or CONFIG["model"]

    watchdog = _find_watchdog()
    if watchdog is None:
        return "Error: no watchdog found — cannot spawn (is praxis watchdog running?)"

    req_payload = {
        "type": "spawn_req",
        "parent_id": BOT_ID,
        "name": new_name,
        "goal": new_goal,
        "model": new_model,
        "brain": new_brain,
        "thinking": thinking_enabled,
        "_secret": GOSSIP_SECRET_OVERRIDE or "",
    }
    if WORKSPACE_NAME:
        req_payload["workspace"] = WORKSPACE_NAME
    if BOT_SCOPE and BOT_SCOPE != "open":
        req_payload["scope"] = BOT_SCOPE
    if ALLOWED_WORKSPACES:
        req_payload["allowed_workspaces"] = ALLOWED_WORKSPACES

    try:
        resp = cluster.send_request(watchdog["id"], GOSSIP_MSG, req_payload)
    except Exception as e:
        return "Error: spawn request failed: " + str(e)
    if resp is None:
        return "Error: spawn request timed out"
    if resp.get("error"):
        return "Spawn error: " + resp["error"]

    state["_spawn_count"] = spawn_count + 1
    _save_state(state)
    _bump_fitness("spawns")
    return "Spawned: " + resp.get("bot_id", new_name) + " (watchdog will start it)"


def _spawn_hybrid(args):
    """Genetic crossover: request brain from peer, merge with own, spawn child."""
    other_id = args["other_bot"]
    new_name = args.get("name") or "hybrid-" + str(uuid.uuid4())[:8]
    new_goal = args["goal"]

    target_node = None
    target_meta = {}
    for n in swarm.nodes():
        meta = n.get("metadata", {})
        if meta.get("id") == other_id:
            target_node = n
            target_meta = meta
            break
    if target_node is None:
        return "Bot not found: " + other_id
    if not _peer_in_scope(target_meta):
        return "Bot not in your scope: " + other_id

    _log("INFO", "-> brain_req  to=" + other_id)
    req_payload = {"type": "brain_req"}
    if GOSSIP_SECRET:
        req_payload["_secret"] = GOSSIP_SECRET
    try:
        resp = cluster.send_request(target_node["id"], GOSSIP_MSG, req_payload)
    except Exception:
        resp = None
    if resp is None:
        return "No brain response from " + other_id + " within 10s."

    other_brain = resp.get("brain", "")
    own_brain = _read_brain()
    merged = "# Hybrid brain: " + BOT_ID + " x " + other_id + "\n\n"
    merged += "## From " + BOT_ID + ":\n" + own_brain + "\n\n"
    merged += "## From " + other_id + ":\n" + other_brain + "\n"
    new_model = args.get("model") or CONFIG["model"]
    return _spawn_bot({"name": new_name, "goal": new_goal, "brain": merged, "model": new_model})


def _evolve_brain(args):
    content = args["content"]
    reason = args.get("reason", "")
    if len(content) > BRAIN_HOT_MAX:
        return (
            "Brain too large for hot memory (max " + str(BRAIN_HOT_MAX) + " chars). "
            "Archive older sections to warm memory using archive_to_warm_memory, then retry."
        )
    old_brain = _read_brain()
    if old_brain == content:
        return "Brain unchanged."
    old_lines = len(old_brain.split("\n"))
    new_lines = len(content.split("\n"))
    _log("EVOLVE", "brain " + str(old_lines) + " -> " + str(new_lines) + " lines")
    history = _read_brain_history()
    history.append({"ts": int(time.time()), "snapshot": _trunc(old_brain, 500), "reason": _trunc(reason, 200)})
    history = history[-MAX_BRAIN_HISTORY:]
    tmp = BRAIN_PATH + ".tmp"
    os.write_file(tmp, content)
    os.rename(tmp, BRAIN_PATH)
    htmp = BRAIN_HISTORY_PATH + ".tmp"
    os.write_file(htmp, json.dumps(history, indent=2))
    os.rename(htmp, BRAIN_HISTORY_PATH)
    _bump_fitness("brain_evolutions")
    return "Brain updated."


def _recall_warm_memory(args):
    query = args.get("query", "")
    warm = _read_warm_memory()
    if not warm:
        return "(warm memory is empty)"
    if query:
        query_lower = query.lower()
        lines = warm.split("\n")
        matched = [l for l in lines if query_lower in l.lower()]
        if not matched:
            return "(no matches for: " + query + ")"
        return "\n".join(matched[:300])
    return warm[:20000]


def _archive_to_warm_memory(args):
    content = args["content"]
    label = args.get("label", "")
    existing = _read_warm_memory()
    header = "\n## " + (label if label else "Archived " + str(int(time.time()))) + "\n"
    new_warm = existing + header + content.rstrip("\n") + "\n"
    if len(new_warm) > MAX_BRAIN_SIZE:
        return "Warm memory would exceed limit (" + str(MAX_BRAIN_SIZE) + " chars). Summarise or clear old sections first."
    _write_warm_memory(new_warm)
    return "Archived to warm memory" + (" under '" + label + "'" if label else "") + "."


def _update_warm_memory(args):
    content = args["content"]
    action = args.get("action", "replace")
    if action == "append":
        existing = _read_warm_memory()
        new_warm = existing + ("\n" if existing else "") + content
    else:
        new_warm = content
    if len(new_warm) > MAX_BRAIN_SIZE:
        return "Warm memory would exceed limit (" + str(MAX_BRAIN_SIZE) + " chars)."
    _write_warm_memory(new_warm)
    return "Warm memory updated."


def _query_model(args):
    target_model = args["model"]
    prompt = args["prompt"]
    system = args.get("system", "")
    use_thinking = args.get("thinking", True)
    try:
        final_prompt = prompt if use_thinking else "/no_think\n" + prompt
        if system:
            return client.ask(target_model, final_prompt, system_prompt=system, max_tokens=4096)
        return client.ask(target_model, final_prompt, max_tokens=4096)
    except Exception as e:
        return "Error querying " + target_model + ": " + str(e)


def _list_models(args):
    if not AVAILABLE_MODELS:
        return "No additional models available."
    return json.dumps(AVAILABLE_MODELS)


def _http_allowed(url):
    if not HTTP_ALLOWLIST:
        return True
    try:
        host = url.split("//", 1)[1].split("/")[0].split(":")[0].lower()
        for allowed in HTTP_ALLOWLIST:
            a = allowed.lower()
            if host == a or host.endswith("." + a):
                return True
    except Exception:
        pass
    return False


def _http_request(args):
    url = args["url"]
    if not _http_allowed(url):
        return "Error: " + url.split("//", 1)[-1].split("/")[0] + " is not in the HTTP allowlist."
    method = args.get("method", "GET").upper()
    body = args.get("body", "")
    content_type = args.get("content_type", "")
    extra_headers = args.get("headers", "")
    timeout = int(args.get("timeout", "30"))
    try:
        hdrs = {}
        if content_type:
            hdrs["Content-Type"] = content_type
        if extra_headers:
            for h in [h.strip() for h in extra_headers.split(",") if h.strip()]:
                if ":" in h:
                    k, v = h.split(":", 1)
                    hdrs[k.strip()] = v.strip()
        if method == "GET":
            resp = requests.get(url, timeout=timeout, headers=hdrs)
        elif method == "POST":
            resp = requests.post(url, data=body, timeout=timeout, headers=hdrs)
        elif method == "PUT":
            resp = requests.put(url, data=body, timeout=timeout, headers=hdrs)
        elif method == "PATCH":
            resp = requests.patch(url, data=body, timeout=timeout, headers=hdrs)
        elif method == "DELETE":
            resp = requests.delete(url, timeout=timeout, headers=hdrs)
        else:
            return "Error: unsupported method: " + method
        return json.dumps({"http_status": resp.status_code, "body": resp.text[:50000]})
    except Exception as e:
        return "Error: " + str(e)


def _ask_consensus(args):
    question = args["question"]
    n = int(args.get("n", 3))
    if n < 1 or n % 2 == 0:
        return "n must be a positive odd number"

    members = swarm.nodes()
    alive = [m for m in members if m.get("metadata", {}).get("id") != BOT_ID and _peer_in_scope(m.get("metadata", {}))]
    if len(alive) < n:
        return "Not enough peers (" + str(len(alive)) + " alive, need " + str(n) + ")"

    targets = list(alive)
    random.shuffle(targets)
    targets = targets[:n]

    target_ids = [t.get("metadata", {}).get("id", t["id"][:8]) for t in targets]
    _log("INFO", "-> consensus_req  to=" + ", ".join(target_ids) + "  q=" + _trunc(question, 80))

    req_payload = {
        "type": "consensus_req",
        "question": question,
        "from": BOT_ID,
    }
    if GOSSIP_SECRET:
        req_payload["_secret"] = GOSSIP_SECRET

    responses = {}
    for target in targets:
        try:
            resp = cluster.send_request(target["id"], GOSSIP_MSG, req_payload)
            if resp:
                from_id = resp.get("from", target.get("metadata", {}).get("id", target["id"][:8]))
                responses[from_id] = resp.get("answer", "")
        except Exception:
            pass

    if not responses:
        return "No responses received."

    counts = {}
    for r in responses.values():
        counts[r] = counts.get(r, 0) + 1
    majority = max(counts, key=counts.get)
    max_count = counts[majority]

    try:
        mem.remember("Consensus: " + question + " Majority: " + majority + " (" + str(max_count) + "/" + str(len(responses)) + ")")
    except Exception:
        pass
    _bump_fitness("consensus_asked")

    return json.dumps({
        "question": question,
        "responses": responses,
        "majority": majority,
        "agreement": str(max_count) + "/" + str(len(responses)),
    })


def _memory_remember(args):
    content = args["content"]
    mtype = args.get("type", "note")
    importance = float(args.get("importance", 0.5))
    try:
        result = mem.remember(content, type=mtype, importance=importance)
        return json.dumps(result)
    except Exception as e:
        return "Error: " + str(e)


def _memory_recall(args):
    query = args.get("query", "")
    limit = int(args.get("limit", 10))
    mtype = args.get("type", "")
    try:
        results = mem.recall(query=query, limit=limit, type=mtype)
        _log("MEM", "recall " + str(len(results)) + " results")
        return json.dumps(results)
    except Exception as e:
        return "Error: " + str(e)


def _memory_forget(args):
    mid = args["id"]
    try:
        mem.forget(mid)
        return "Forgotten: " + mid
    except Exception as e:
        return "Error: " + str(e)


tools.add("read_file", "Read a file in your directory", {"path": "string"}, _wrap_tool("read_file", _read_file))
tools.add("read_file_range", "Read a line range from a file (1-indexed)", {"path": "string", "start": "int", "end": "int?"}, _wrap_tool("read_file_range", _read_file_range))
tools.add("write_file", "Write a file to your directory. Include a brief description of what the file contains.", {"path": "string", "content": "string", "description": "string?"}, _wrap_tool("write_file", _write_file))
tools.add("append_file", "Append content to an existing file (creates it if absent)", {"path": "string", "content": "string"}, _wrap_tool("append_file", _append_file))
tools.add("delete_file", "Delete a file from your directory", {"path": "string"}, _wrap_tool("delete_file", _delete_file))
tools.add("replace_in_file", "Replace text in a file (more efficient than read+write for small edits)", {"path": "string", "old": "string", "new": "string"}, _wrap_tool("replace_in_file", _replace_in_file))
tools.add("list_dir", "List files in a directory", {"path": "string?"}, _wrap_tool("list_dir", _list_dir))
tools.add("search", "Search file contents with a regex pattern", {"pattern": "string", "path": "string?", "glob": "string?", "ignore_case": "bool?"}, _wrap_tool("search", _search))
tools.add("shell", "Run a shell command via the command proxy (sandboxed, no network tools)", {"command": "string", "cwd": "string?", "timeout": "int?"}, _wrap_tool("shell", _shell))
tools.add("run_script", "Run a scriptling script", {"path": "string", "args": "string?"}, _wrap_tool("run_script", _run_script))
tools.add("send_message", "Send a direct message to a bot by ID", {"recipient": "string", "content": "string"}, _wrap_tool("send_message", _send_message))
tools.add("complete_task", "Report task completion to your parent bot", {"parent_bot": "string", "result": "string", "task_id": "string?"}, _wrap_tool("complete_task", _complete_task))
tools.add("read_messages", "Read your unread messages", {}, _wrap_tool("read_messages", _read_messages))
tools.add("list_bots", "List all bots visible in the swarm", {}, _wrap_tool("list_bots", _list_bots))
tools.add("spawn_bot", "Create a new autonomous child bot", {"goal": "string", "name": "string?", "brain": "string?", "model": "string?", "task_id": "string?"}, _wrap_tool("spawn_bot", _spawn_bot))
tools.add("spawn_hybrid", "Crossover your brain with another bot's to create a child", {"other_bot": "string", "goal": "string", "name": "string?", "model": "string?"}, _wrap_tool("spawn_hybrid", _spawn_hybrid))
tools.add("evolve_brain", "Rewrite your brain (hot memory, max 8 KB). Include a reason. Archive older content to warm memory first if brain is too large.", {"content": "string", "reason": "string?"}, _wrap_tool("evolve_brain", _evolve_brain))
tools.add("recall_warm_memory", "Search warm memory (memory.md) for relevant content. Pass a query to filter, or omit to read all.", {"query": "string?"}, _wrap_tool("recall_warm_memory", _recall_warm_memory))
tools.add("archive_to_warm_memory", "Move content from your brain to warm memory (memory.md) under an optional label. Use before shrinking brain.md.", {"content": "string", "label": "string?"}, _wrap_tool("archive_to_warm_memory", _archive_to_warm_memory))
tools.add("update_warm_memory", "Overwrite or append to warm memory directly.", {"content": "string", "action": "string?"}, _wrap_tool("update_warm_memory", _update_warm_memory))
tools.add("query_model", "Send a one-shot prompt to a specific model (for specialised subtasks)", {"model": "string", "prompt": "string", "system": "string?", "thinking": "bool?"}, _wrap_tool("query_model", _query_model))
tools.add("list_models", "List available models with descriptions, costs, and strengths", {}, _wrap_tool("list_models", _list_models))
tools.add("http_request", "HTTP request (GET/POST/PUT/DELETE/PATCH) with optional body and headers", {"url": "string", "method": "string?", "body": "string?", "content_type": "string?", "headers": "string?", "timeout": "int?"}, _wrap_tool("http_request", _http_request))
tools.add("ask_consensus", "Ask other bots for their opinion and return the majority (use sparingly, only when you truly need a second opinion)", {"question": "string", "n": "int?"}, _wrap_tool("ask_consensus", _ask_consensus))
tools.add("memory_remember", "Store a fact, lesson, or observation in persistent memory. Include a reason explaining why this is worth remembering.", {"content": "string", "type": "string?", "importance": "float?", "reason": "string?"}, _wrap_tool("memory_remember", _memory_remember))
tools.add("memory_recall", "Search memories by keyword and similarity, or call with no args to load your preferences and top memories", {"query": "string?", "limit": "int?", "type": "string?"}, _wrap_tool("memory_recall", _memory_recall))
tools.add("memory_forget", "Remove a memory by ID", {"id": "string"}, _wrap_tool("memory_forget", _memory_forget))


def _build_system_prompt():
    brain = _read_brain()
    history = _read_brain_history()

    prompt = "You are " + BOT_ID + ", an autonomous agent. Your goal drives everything you do.\n\n"
    prompt += "## Goal\n" + goal + "\n\n"
    prompt += "## How you work\n"
    prompt += "Each tick, take the next meaningful action toward your goal.\n"
    prompt += "Store knowledge, decisions, and plans as files under entities/ - write them, refine them, use them.\n"
    prompt += "When you need to automate or compute something, write a scriptling script under entities/ and run it.\n"
    prompt += "When you need more capacity, spawn child bots with specific sub-goals. Pick a model that suits the child's task — a cheaper model for routine work, a stronger one for complex reasoning.\n"
    prompt += "You decide what to create, write, or delegate. No one tells you what you need.\n\n"
    prompt += "## Persistence\n"
    prompt += "You have three ways to persist information. Each serves a different purpose:\n"
    prompt += "- **Brain** (brain.md via evolve_brain): YOUR BEHAVIOUR — strategies, working patterns, lessons about HOW you work, what to do differently. Update your brain when you learn a better approach, discover a mistake in your process, or want to change how you operate. This is always in your system prompt.\n"
    prompt += "- **Memory** (memory_remember / memory_recall): KNOWLEDGE — facts, observations, events, preferences. Things you KNOW but that don't change how you work. Searchable, auto-deduplicated, decays by type.\n"
    prompt += "  - preference: never decays (themes, formats, styles)\n"
    prompt += "  - fact: 90-day half-life (names, IDs, limits, config)\n"
    prompt += "  - event: 30-day half-life (things that happened)\n"
    prompt += "  - note: 7-day half-life (transient observations, default)\n"
    prompt += "- **Files** (entities/): STRUCTURED WORK PRODUCTS — plans, scripts, data, documents. Full control over format.\n\n"
    prompt += "Rule of thumb: if it changes HOW you work, update your brain. If it's something you KNOW, store in memory.\n"
    prompt += "You should evolve your brain regularly as you learn — don't only use memory.\n\n"
    prompt += "## File organisation\n"
    prompt += "entities/ is YOUR knowledge and tools — plans, notes, automation scripts, reference data. NOT work output.\n"
    prompt += "Work products (code you're writing, documents you're generating) go outside entities/ via shell or to paths your goal specifies.\n"
    prompt += "Organise entities/ however suits your goal. A full file index is included in each tick message — do NOT duplicate it in your brain or warm memory.\n"
    prompt += "Special: write_file(\"brain.md\", ...) updates your brain (system prompt).\n"
    prompt += "For small edits use replace_in_file(path, old, new) instead of reading and rewriting the whole file.\n"
    prompt += "When writing files, include a brief description: write_file(path, content, description=\"what this file is for\"). These appear in the index.\n\n"
    prompt += "IMPORTANT: Only use the tools provided to you. Do not invent tool names.\n\n"
    prompt += "## Scriptling\n"
    prompt += "Scriptling is your automation language for run_script. Full syntax reference: read_file(\"entities/scriptling-reference.md\").\n"
    prompt += "Key differences from Python: no async/await, no yield, no type annotations, no open()/eval()/exec(). Regex uses RE2 (no backreferences/lookaround).\n"
    prompt += "Scripts start with: #!/usr/bin/env scriptling, scripts extension must be `.py`\n\n"

    if brain:
        prompt += "## Your Brain\n" + brain + "\n\n"

    if history:
        prompt += "## Recent Brain Changes\n"
        for h in history[-3:]:
            ts = time.strftime("%Y-%m-%d %H:%M", time.gmtime(h["ts"]))
            prompt += "- " + ts + ": " + h["snapshot"][:300] + "\n"
        prompt += "\n"

    warm = _read_warm_memory()
    if warm:
        size_kb = max(1, len(warm) // 1024)
        prompt += "## Warm Memory\nYou have " + str(size_kb) + " KB of warm memory available. Use recall_warm_memory to search it.\n\n"

    try:
        prefs = mem.recall(type="preference", limit=-1)
        if prefs:
            prompt += "## Your Preferences\n"
            for p in prefs:
                prompt += "- " + p["content"] + "\n"
            prompt += "\n"
    except Exception:
        pass

    if AVAILABLE_MODELS:
        prompt += "## Available Models\n"
        prompt += "You are running on: " + model_name + "\n\n"
        prompt += "Use `query_model(model, prompt)` for one-shot calls or `model=` in spawn_bot/spawn_hybrid to assign a model to a child. Choose a model that fits the child's task — don't default to your own model if another is better suited.\n\n"
        for m in AVAILABLE_MODELS:
            prompt += "- **" + m["id"] + "**"
            if m.get("label"):
                prompt += " (" + m["label"] + ")"
            prompt += " - " + m.get("description", "")
            if m.get("cost"):
                prompt += " Cost: " + m["cost"] + "."
            if m.get("strengths"):
                prompt += " Strengths: " + ", ".join(m["strengths"]) + "."
            prompt += "\n"
        prompt += "\n"

    prompt += "## Handling messages\n"
    prompt += "Unread messages are included directly in your tick message — act on them immediately:\n"
    prompt += "- A task request (\"write X\", \"build Y\", \"analyse Z\") — do it, then reply with the result.\n"
    prompt += "- A question — answer it directly.\n"
    prompt += "- Coordination from a peer — incorporate it and continue.\n"
    prompt += "- A task_complete report — incorporate the result and continue.\n"
    prompt += "Never wait for further clarification before starting. If the request is clear enough to attempt, attempt it.\n"
    prompt += "Do NOT log that you are 'awaiting' or 'unsure whether to proceed' — just proceed.\n\n"
    prompt += "## Communication scope\n"
    prompt += "Your scope controls which bots you can see and message. Your scope is shown in each tick message.\n"
    prompt += "- **open**: You see all bots. Send messages directly.\n"
    prompt += "- **isolated**: You only see bots in your workspace. No cross-workspace messaging.\n"
    prompt += "- **gateway**: You see your workspace peers plus bots in your allowed workspaces. Cross-workspace messages are relayed through the watchdog automatically — just use send_message as normal.\n"
    prompt += "- **family**: You only see your parent and children. For tightly-coupled task delegation.\n"
    prompt += "Incoming consensus requests and relayed messages always reach you regardless of scope.\n\n"
    prompt += "## Directives\n"
    prompt += "- Act autonomously each tick. No approval needed.\n"
    prompt += "- All files go under entities/ - never write to bare filenames in the root.\n"
    prompt += "- Communicate only when it serves your goals.\n"
    prompt += "- Evolve your brain to reflect what you've learned and what works.\n"
    prompt += "- Use spawn_hybrid to cross-pollinate strategies with peers.\n"
    prompt += "- Use ask_consensus sparingly - only when you truly need a second opinion.\n"
    prompt += "- When spawned with a task_id, call complete_task(parent_bot, task_id, result) when done.\n"
    return prompt


bot_agent = agent.Agent(
    client,
    tools=tools,
    system_prompt=_build_system_prompt(),
    model=model_name,
    max_tokens=AGENT_MAX_TOKENS,
    compaction_threshold=AGENT_COMPACTION_THRESHOLD,
)
bot_agent.request_timeout_ms = AGENT_REQUEST_TIMEOUT_MS

# --- Main loop ---
_log("START", "bot started  model=" + model_name + "  scope=" + BOT_SCOPE + "  workspace=" + (WORKSPACE_PATH or "none"))
_flush_log()

_tick_count = 0
_consecutive_errors = 0

while True:
    status = _safe_read_json(STATUS_PATH)
    if status and status.get("status") == "stopping":
        _atomic_write_json(STATUS_PATH, _build_status("stopped"))
        cluster.stop()
        break

    if _tick_count == 0 and _find_watchdog() is None:
        _log("INFO", "waiting for watchdog...")
        _flush_log()
        time.sleep(2)

    _tick_sleep = TICK_INTERVAL
    try:
        global _state_cache
        _state_cache = None
        state = _load_state()
        entity_count = _count_entities()
        fitness = state.get("fitness", {})
        _tick_count += 1
        _tick_start = time.time()
        del _log_buffer[:]

        if _tick_count % MULTICAST_ANNOUNCE_EVERY == 0:
            try:
                mg = mc.join(MULTICAST_ADDR, MULTICAST_PORT)
                mg.send({"type": "announce", "gossip_addr": _gossip_addr, "id": BOT_ID})
                mg.close()
            except Exception:
                pass

        if _tick_count % 100 == 1:
            try:
                _rebuild_index()
            except Exception:
                pass

        unread_msgs = []
        while _inbox.size() > 0:
            unread_msgs.append(_inbox.get())

        _log("INFO", "tick " + str(_tick_count) + "  swarm=" + str(cluster.num_alive()) + "  unread=" + str(len(unread_msgs)) + "  fitness=" + json.dumps(fitness))
        _flush_log()

        tick_msg = "## Context\n"
        tick_msg += "Tick: " + str(_tick_count) + "  Model: " + model_name + "  Swarm: " + str(cluster.num_alive()) + "  Scope: " + BOT_SCOPE + "\n"
        tick_msg += "Fitness: " + json.dumps(fitness) + "\n"
        if election.is_leader():
            tick_msg += "Role: swarm leader\n"
        if WORKSPACE_PATH:
            tick_msg += "Workspace: " + WORKSPACE_PATH + "\n"

        file_listing = _build_file_listing()
        if file_listing:
            tick_msg += "\n" + file_listing

        last_activity = state.get("_last_activity", "")
        if last_activity:
            tick_msg += "\nLast tick you: " + last_activity + "\n"

        tick_msg += "\n## Instructions\n"
        if unread_msgs:
            tick_msg += "You have " + str(len(unread_msgs)) + " message" + ("" if len(unread_msgs) == 1 else "s") + ". Act on them immediately.\n\n"
            for m in unread_msgs:
                sender = m.get("from", "?")
                content = m.get("content", "")
                mtype = m.get("type", "message")
                if mtype == "task_complete":
                    tick_msg += "From " + sender + " [task_complete]: task_id=" + m.get("task_id", "") + " result=" + content + "\n"
                elif mtype == "ack":
                    tick_msg += "From " + sender + " [delivered]: your message was received. They will respond on their next tick.\n"
                else:
                    tick_msg += "From " + sender + ": " + content + "\n"
            tick_msg += "\nWhat is your next action?"
        else:
            tick_msg += "What is your next action toward your goal?"

        _lock_model()
        _log("INFO", "calling LLM " + model_name + "...")
        _flush_log()
        try:
            sys_prompt = _build_system_prompt()
            bot_agent.system_prompt = sys_prompt
            trigger_msg = tick_msg if thinking_enabled else "/no_think\n" + tick_msg
            if LOG_VERBOSE:
                debug_dir = os.path.join(BOT_DIR, "debug")
                if not os.path.exists(debug_dir):
                    os.makedirs(debug_dir)
                os.write_file(os.path.join(debug_dir, "tick-" + str(_tick_count) + ".md"), "# System Prompt\n\n" + sys_prompt + "\n\n# Tick Message\n\n" + trigger_msg + "\n")
            bot_agent.trigger(trigger_msg, max_iterations=TICK_MAX_ITERATIONS)
        finally:
            _unlock_model()

        _bump_fitness("ticks_alive")
        _atomic_write_json(STATUS_PATH, _build_status())

        activity = _summarize_tick(_log_buffer)
        if activity:
            state = _load_state()
            state["_last_activity"] = _trunc(activity, 500)
            _save_state(state)

        elapsed = str(int((time.time() - _tick_start) * 1000)) + "ms"
        fitness = _load_state().get("fitness", {})
        _log("INFO", "tick " + str(_tick_count) + " done  elapsed=" + elapsed + "  fitness=" + json.dumps(fitness))
        _flush_log()
        _consecutive_errors = 0

    except Exception as e:
        _consecutive_errors += 1
        _tick_sleep = min(TICK_INTERVAL * (2 ** min(_consecutive_errors - 1, 4)), MAX_BACKOFF_SEC)
        _log("ERROR", "tick " + str(_tick_count) + " failed: " + str(e) + "  backoff=" + str(_tick_sleep) + "s")
        _flush_log()

    time.sleep(_tick_sleep)
