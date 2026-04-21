#!/usr/bin/env scriptling

# --- BOT CONFIG ---

CONFIG = {
    "name": "TEMPLATE_NAME",
    "goal": "TEMPLATE_GOAL",
    "model": "TEMPLATE_MODEL",
    "brain": "TEMPLATE_BRAIN",
    "seed_addrs": [],
    "thinking": True
}
# --- END CONFIG ---


import scriptling.ai as ai
import scriptling.ai.agent as agent
import scriptling.ai.memory as memory
import scriptling.runtime as runtime
import scriptling.runtime.kv as kv
import scriptling.net.gossip as gossip
import scriptling.net.multicast as mc
import os
import os.path
import json
import uuid
import time
import random
import subprocess
import requests
import glob as _glob

BOT_DIR = os.path.dirname(os.path.abspath(__file__))
BOT_ID = CONFIG["name"]
BOTS_DIR = os.path.dirname(BOT_DIR)
LOCKS_DIR = os.path.join(os.path.dirname(BOTS_DIR), ".locks")

_BWRAP_AVAILABLE = subprocess.run(["which", "bwrap"], capture_output=True).returncode == 0

STATE_PATH = os.path.join(BOT_DIR, "state.json")
STATUS_PATH = os.path.join(BOT_DIR, "status.json")
ERROR_LOG = os.path.join(BOT_DIR, "errors.log")
ACTIVITY_LOG = os.path.join(BOT_DIR, "activity.log")
# --- DEFAULTS ---

ACTIVITY_LOG_MAX = 100 * 1024
MAX_BRAIN_SIZE = 50000
MAX_SPAWN_COUNT = 10
MAX_BRAIN_HISTORY = 5
MULTICAST_ADDR = "239.255.13.37"
MULTICAST_PORT = 19373
MULTICAST_ANNOUNCE_EVERY = 10
AGENT_MAX_TOKENS = 50000
AGENT_COMPACTION_THRESHOLD = 70
AGENT_REQUEST_TIMEOUT_MS = 300000
GOSSIP_SECRET = ""
TICK_INTERVAL = 30
STALE_THRESHOLD_SEC = 120
SCRIPT_TIMEOUT = 30
MAX_BACKOFF_SEC = 600
BOT_MAX_CONCURRENT = 1
TICK_MAX_ITERATIONS = 5
HTTP_ALLOWLIST = []
SHELL_ALLOWLIST = []
# --- END DEFAULTS ---

# --- MODELS ---
AVAILABLE_MODELS = []
# --- END MODELS ---

GOSSIP_MSG = gossip.MSG_USER    # 128 - all custom payloads dispatched by payload["type"]


# --- Helpers ---
def _log(level, msg):
    print(time.strftime("%Y-%m-%d %H:%M:%S") + " [" + level + "] [" + BOT_ID + "] " + msg)


def _trunc(s, n):
    if LOG_VERBOSE:
        return s
    return s[:n] + "..." if len(s) > n else s


def _log_error(msg):
    try:
        entry = "[" + time.strftime("%Y-%m-%d %H:%M:%S") + "] " + msg + "\n"
        existing = ""
        if os.path.exists(ERROR_LOG):
            existing = os.read_file(ERROR_LOG)
        os.write_file(ERROR_LOG, existing + entry)
    except Exception:
        pass


# Per-tick activity buffer - flushed to activity.log at tick end
_activity_buffer = []


def _log_activity(line):
    _activity_buffer.append(line)


def _flush_activity():
    if not _activity_buffer:
        return
    try:
        block = "\n".join(_activity_buffer) + "\n"
        del _activity_buffer[:]
        existing = ""
        if os.path.exists(ACTIVITY_LOG):
            existing = os.read_file(ACTIVITY_LOG)
        combined = existing + block
        if len(combined) > ACTIVITY_LOG_MAX:
            combined = combined[-ACTIVITY_LOG_MAX:]
        os.write_file(ACTIVITY_LOG, combined)
    except Exception:
        pass


def _wrap_tool(name, fn):
    def wrapper(args):
        _log_activity("  TOOL " + name)
        for k, v in args.items():
            s = str(v).replace("\n", " ")
            _log_activity("       " + k + ": " + _trunc(s, 120))
        result = fn(args)
        summary = str(result).replace("\n", " ")
        prefix = "  ERROR" if (summary.startswith("Error") or summary.startswith("{\"exit_code\": 1") or "exit_code\":1" in summary) else "    OK"
        _log_activity(prefix + "  " + _trunc(summary, 160))
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

    # Ticket name: zero-padded ms timestamp + BOT_ID for stable sort and tiebreaking
    ticket = str(int(time.time() * 1000)).zfill(16) + "_" + BOT_ID + ".wait"
    ticket_file = os.path.join(queue_dir, ticket)
    os.write_file(ticket_file, str(int(time.time())))

    while True:
        try:
            now = int(time.time())
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
                # Ticket was removed (e.g. stale cleanup race) — re-register
                os.write_file(ticket_file, str(int(time.time())))
                continue
            if pos < limit:
                return
            _log("INFO", "LLM queue pos=" + str(pos + 1) + "/" + str(len(entries)) + " model=" + model_name + " limit=" + str(limit))
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
    if rel_path.replace("\\", "/").startswith("/"):
        return None
    return os.path.join(base_dir, rel_path)


def _inject_config(source, config):
    config_json = json.dumps(config, indent=4)
    config_json = config_json.replace(":true", ":True").replace(":false", ":False").replace(":null", ":None")
    config_json = config_json.replace(": true", ": True").replace(": false", ": False").replace(": null", ": None")
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


def _bump_fitness(key, delta=1):
    state = _load_state()
    fitness = state.setdefault("fitness", {})
    fitness[key] = fitness.get(key, 0) + delta
    _save_state(state)


def _detect_local_ip():
    try:
        result = subprocess.run(
            ["hostname", "-I"],
            capture_output=True,
            timeout=5,
        )
        if result.returncode == 0 and result.stdout:
            ip = result.stdout.strip().split()[0]
            if ip and ip != "0.0.0.0":
                return ip
    except Exception:
        pass
    return "0.0.0.0"


# --- Network startup ---
api_key = os.environ.get("BOT_API_KEY", "")
base_url = os.environ.get("BOT_BASE_URL", "")
model_name = CONFIG["model"] or os.environ.get("BOT_MODEL", "")
goal = CONFIG["goal"]
seed_addrs = CONFIG.get("seed_addrs", [])
thinking_enabled = CONFIG.get("thinking", True)

_start_time = int(time.time())


# In-memory inbox - gossip handler pushes here, read_messages() drains it
_inbox = runtime.sync.Queue("inbox-" + BOT_ID, maxsize=200)

# brain_req/resp rendezvous: request_id -> brain string
_brain_responses = {}

# consensus_req/resp rendezvous: request_id -> {bot_id: answer}
_consensus_responses = {}

# pending consensus requests to process at tick time (avoids blocking gossip goroutine)
_consensus_req_queue = runtime.sync.Queue("consensus-req-" + BOT_ID, maxsize=50)

# Use a stable port: persisted in state so restarts reuse the same address
_state_init = _load_state()
if not _state_init.get("_gossip_port"):
    _state_init["_gossip_port"] = random.randint(20000, 59999)
    _save_state(_state_init)
_gossip_port = _state_init["_gossip_port"]

cluster = gossip.create(bind_addr="0.0.0.0:" + str(_gossip_port))
cluster.start()
_gossip_addr = _detect_local_ip() + ":" + str(_gossip_port)
cluster.set_metadata("id", BOT_ID)
cluster.set_metadata("goal", goal)
_log("INFO", "started  addr=" + _gossip_addr + "  model=" + model_name)


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
            return

    msg_type = payload.get("type", "message")
    sender_meta = msg.get("sender", {}).get("metadata", {})
    sender_id = sender_meta.get("id", "")
    if not sender_id:
        raw = msg.get("sender", {}).get("id", "")
        sender_id = raw[:16] if raw else "?"
    sender_node_id = msg.get("sender", {}).get("id", "")

    if msg_type == "message":
        content = payload.get("content", "")
        _inbox.put({
            "from": sender_id,
            "content": content,
            "ts": int(time.time()),
        })
        _log("INFO", "<- message  from=" + sender_id + "  content=" + _trunc(content, 100))

    elif msg_type == "brain_req":
        req_id = payload.get("request_id", "")
        if req_id and sender_node_id:
            _log("INFO", "<- brain_req  from=" + sender_id)
            _gossip_send(sender_node_id, {
                "type": "brain_resp",
                "request_id": req_id,
                "brain": _read_brain(),
            })
            _log("INFO", "-> brain_resp  to=" + sender_id)

    elif msg_type == "brain_resp":
        req_id = payload.get("request_id", "")
        if req_id:
            _brain_responses[req_id] = payload.get("brain", "")
            _log("INFO", "<- brain_resp  from=" + sender_id)

    elif msg_type == "consensus_req":
        req_id = payload.get("request_id", "")
        question = payload.get("question", "")
        if req_id and sender_node_id and question:
            _log("INFO", "<- consensus_req  from=" + sender_id + "  q=" + _trunc(question, 80))
            _consensus_req_queue.put({
                "req_id": req_id,
                "question": question,
                "sender_node_id": sender_node_id,
                "sender_id": sender_id,
            })

    elif msg_type == "consensus_resp":
        req_id = payload.get("request_id", "")
        answer = payload.get("answer", "")
        from_id = payload.get("from", "")
        if req_id and from_id:
            if req_id not in _consensus_responses:
                _consensus_responses[req_id] = {}
            _consensus_responses[req_id][from_id] = answer
            _log("INFO", "<- consensus_resp  from=" + from_id + "  answer=" + _trunc(answer, 80))

    elif msg_type == "task_complete":
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

    elif msg_type == "stop":
        _log("WARN", "<- stop  from=" + sender_id)
        _atomic_write_json(STATUS_PATH, _build_status("stopping"))


cluster.handle(GOSSIP_MSG, _on_gossip_msg)
cluster.set_metadata("gossip_addr", _gossip_addr)

# Join cluster: seeds first, then multicast discovery
_joined = False
if seed_addrs:
    try:
        cluster.join(seed_addrs)
        _joined = True
    except Exception as e:
        _log_error("Seed join failed: " + str(e))

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
        _log_error("Multicast discovery failed: " + str(e))

# Announce presence so future bots on the subnet can find us
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

_migrate_state()

if os.environ.get("BOT_SHELL_SANDBOX", "true").lower() != "false" and not _BWRAP_AVAILABLE:
    _log("WARN", "bwrap not found — shell tool running unsandboxed. Install bwrap or set BOT_SHELL_SANDBOX=false to silence this.")

initial_brain = CONFIG.get("brain", "")
if initial_brain and not os.path.exists(BRAIN_PATH):
    tmp = BRAIN_PATH + ".tmp"
    os.write_file(tmp, initial_brain)
    os.rename(tmp, BRAIN_PATH)


def _build_status(status="running"):
    state = _load_state()
    return {
        "id": BOT_ID,
        "goal": goal,
        "status": status,
        "gossip_addr": _gossip_addr,
        "started_at": _start_time,
        "last_tick_ts": int(time.time()),
        "fitness": state.get("fitness", {}),
    }


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
    if path == "brain.md":
        return _evolve_brain({"content": content})
    real_path = _safe_path(BOT_DIR, path)
    if real_path is None:
        return "Invalid path: traversal detected."
    dir_name = os.path.dirname(real_path)
    if dir_name and not os.path.exists(dir_name):
        os.makedirs(dir_name)
    os.write_file(real_path, content)
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


_SHELL_BLOCKED = ("curl", "wget")


def _build_bwrap_cmd(command, cwd):
    if os.environ.get("BOT_SHELL_SANDBOX", "true").lower() == "false":
        return None
    if not _BWRAP_AVAILABLE:
        return None

    try:
        rel = os.path.relpath(cwd, BOT_DIR)
        inner_cwd = "/" if rel.startswith("..") else ("/" + rel).rstrip("/") or "/"
    except Exception:
        inner_cwd = "/"

    cmd = ["bwrap", "--chdir", inner_cwd, "--bind", BOT_DIR, "/"]

    for sysdir in ("/usr", "/bin", "/sbin", "/lib", "/lib64", "/lib32", "/etc", "/tmp"):
        if not os.path.exists(sysdir):
            continue
        try:
            rlink = subprocess.run(["readlink", sysdir], capture_output=True, timeout=5)
            if rlink.returncode == 0:
                cmd += ["--symlink", rlink.stdout.strip(), sysdir]
            elif sysdir == "/tmp":
                cmd += ["--bind", sysdir, sysdir]
            else:
                cmd += ["--ro-bind", sysdir, sysdir]
        except Exception:
            cmd += ["--ro-bind", sysdir, sysdir]

    cmd += ["--proc", "/proc", "--dev", "/dev"]

    for mount in [m.strip() for m in os.environ.get("BOT_SHELL_MOUNTS", "").split(",") if m.strip()]:
        parts = mount.split(":", 2)
        if len(parts) == 3:
            mode, host, container = parts[0], parts[1], parts[2]
            if os.path.exists(host):
                cmd += ["--ro-bind" if mode == "ro" else "--bind", host, container]

    cmd += ["--", "bash", "-c", command]
    return cmd


def _shell(args):
    command = args["command"]
    stripped = command.strip().lstrip("/ \t")
    first_word = stripped.split()[0].split("/")[-1] if stripped.split() else ""
    if first_word in _SHELL_BLOCKED:
        return "Error: " + first_word + " is not allowed in shell. Use the http_request tool instead."
    if SHELL_ALLOWLIST and first_word not in SHELL_ALLOWLIST:
        return "Error: " + first_word + " is not in the shell allowlist (" + ", ".join(SHELL_ALLOWLIST) + ")."
    timeout = int(args.get("timeout", SCRIPT_TIMEOUT))
    cwd = BOT_DIR
    if args.get("cwd"):
        cwd = _safe_path(BOT_DIR, args["cwd"]) or BOT_DIR
    bwrap_cmd = _build_bwrap_cmd(command, cwd)
    try:
        if bwrap_cmd:
            result = subprocess.run(bwrap_cmd, capture_output=True, timeout=timeout)
        else:
            result = subprocess.run(command, shell=True, capture_output=True, timeout=timeout, cwd=cwd)
        output = {"exit_code": result.returncode}
        if result.stdout:
            output["stdout"] = str(result.stdout)[:50000]
        if result.stderr:
            output["stderr"] = str(result.stderr)[:10000]
        return json.dumps(output)
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
        cmd = ["rg", "--line-number", "--no-heading", "--color=never"]
        if ignore_case:
            cmd.append("-i")
        if glob_pat:
            cmd.extend(["-g", glob_pat])
        cmd.extend([pattern, real_path])
        result = subprocess.run(cmd, capture_output=True, timeout=30)
        if result.returncode == 2:
            raise Exception("rg error")
        out = str(result.stdout).strip() if result.stdout else "(no matches)"
        return out.replace(BOT_DIR + "/", "")[:20000]
    except Exception:
        pass
    try:
        cmd = ["grep", "-rn", "--color=never"]
        if ignore_case:
            cmd.append("-i")
        if glob_pat:
            cmd.extend(["--include=" + glob_pat])
        cmd.extend([pattern, real_path])
        result = subprocess.run(cmd, capture_output=True, timeout=30)
        out = str(result.stdout).strip() if result.stdout else "(no matches)"
        return out.replace(BOT_DIR + "/", "")[:20000]
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


def _send_message(args):
    recipient = args["recipient"]
    content = args["content"]
    target_node = None
    for n in cluster.alive_nodes():
        if n.get("metadata", {}).get("id") == recipient:
            target_node = n
            break
    if target_node is None:
        return "Bot not found or offline: " + recipient
    _gossip_send(target_node["id"], {
        "type": "message",
        "from": BOT_ID,
        "content": content,
    })
    _log("INFO", "-> message  to=" + recipient + "  content=" + _trunc(content, 100))
    _bump_fitness("messages_sent")
    return "Sent to " + recipient


def _complete_task(args):
    parent_id = args["parent_bot"]
    task_id = args.get("task_id", "")
    result_text = args["result"]
    target_node = None
    for n in cluster.alive_nodes():
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
    for n in cluster.alive_nodes():
        meta = n.get("metadata", {})
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
    new_dir = os.path.join(BOTS_DIR, new_name)
    if os.path.exists(new_dir):
        return "Bot already exists: " + new_name
    os.makedirs(new_dir)
    child_config = {
        "name": new_name,
        "goal": new_goal,
        "model": new_model,
        "brain": new_brain,
        "seed_addrs": [_gossip_addr],
        "thinking": thinking_enabled,
    }
    own_source = os.read_file(os.path.join(BOT_DIR, "bot.py"))
    new_source = _inject_config(own_source, child_config)
    if new_source is None:
        return "Error: source template corrupt."
    os.write_file(os.path.join(new_dir, "bot.py"), new_source)
    _atomic_write_json(os.path.join(new_dir, "status.json"), {
        "id": new_name,
        "goal": new_goal,
        "status": "created",
        "created_at": int(time.time()),
        "gossip_addr": "",
        "fitness": {},
    })
    state["_spawn_count"] = spawn_count + 1
    _save_state(state)
    _bump_fitness("spawns")
    return "Spawned: " + new_name + " (watchdog will start it)"


def _spawn_hybrid(args):
    """Genetic crossover: request brain from peer, merge with own, spawn child."""
    other_id = args["other_bot"]
    new_name = args.get("name") or "hybrid-" + str(uuid.uuid4())[:8]
    new_goal = args["goal"]

    target_node = None
    for n in cluster.alive_nodes():
        if n.get("metadata", {}).get("id") == other_id:
            target_node = n
            break
    if target_node is None:
        return "Bot not found: " + other_id

    req_id = str(uuid.uuid4())
    _gossip_send(target_node["id"], {
        "type": "brain_req",
        "request_id": req_id,
    })
    _log("INFO", "-> brain_req  to=" + other_id)

    deadline = time.time() + 10
    while time.time() < deadline:
        if req_id in _brain_responses:
            break
        time.sleep(0.2)

    other_brain = _brain_responses.pop(req_id, None)
    if other_brain is None:
        return "No brain response from " + other_id + " within 10s."

    own_brain = _read_brain()
    merged = "# Hybrid brain: " + BOT_ID + " x " + other_id + "\n\n"
    merged += "## From " + BOT_ID + ":\n" + own_brain + "\n\n"
    merged += "## From " + other_id + ":\n" + other_brain + "\n"
    new_model = args.get("model") or CONFIG["model"]
    return _spawn_bot({"name": new_name, "goal": new_goal, "brain": merged, "model": new_model})


def _evolve_brain(args):
    content = args["content"]
    if len(content) > MAX_BRAIN_SIZE:
        return "Brain too large (max " + str(MAX_BRAIN_SIZE) + " chars)."
    old_brain = _read_brain()
    if old_brain == content:
        return "Brain unchanged."
    history = _read_brain_history()
    history.append({"ts": int(time.time()), "snapshot": _trunc(old_brain, 500)})
    history = history[-MAX_BRAIN_HISTORY:]
    tmp = BRAIN_PATH + ".tmp"
    os.write_file(tmp, content)
    os.rename(tmp, BRAIN_PATH)
    htmp = BRAIN_HISTORY_PATH + ".tmp"
    os.write_file(htmp, json.dumps(history, indent=2))
    os.rename(htmp, BRAIN_HISTORY_PATH)
    _bump_fitness("brain_evolutions")
    return "Brain updated."


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

    alive = [node for node in cluster.alive_nodes()
             if node.get("metadata", {}).get("id") != BOT_ID]
    if len(alive) < n:
        return "Not enough peers (" + str(len(alive)) + " alive, need " + str(n) + ")"

    targets = list(alive)
    random.shuffle(targets)
    targets = targets[:n]

    req_id = str(uuid.uuid4())
    target_ids = [t.get("metadata", {}).get("id", t["id"][:8]) for t in targets]
    _log("INFO", "-> consensus_req  to=" + ", ".join(target_ids) + "  q=" + _trunc(question, 80))
    for target in targets:
        _gossip_send(target["id"], {
            "type": "consensus_req",
            "request_id": req_id,
            "question": question,
            "from": BOT_ID,
        })

    deadline = time.time() + 15
    while time.time() < deadline:
        if req_id in _consensus_responses and len(_consensus_responses[req_id]) >= n:
            break
        time.sleep(0.5)

    responses = _consensus_responses.pop(req_id, {})
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


tools.add("read_file", "Read a file in your directory", {"path": "string"}, _wrap_tool("read_file", _read_file))
tools.add("read_file_range", "Read a line range from a file (1-indexed)", {"path": "string", "start": "int", "end": "int?"}, _wrap_tool("read_file_range", _read_file_range))
tools.add("write_file", "Write a file to your directory", {"path": "string", "content": "string"}, _wrap_tool("write_file", _write_file))
tools.add("append_file", "Append content to an existing file (creates it if absent)", {"path": "string", "content": "string"}, _wrap_tool("append_file", _append_file))
tools.add("delete_file", "Delete a file from your directory", {"path": "string"}, _wrap_tool("delete_file", _delete_file))
tools.add("list_dir", "List files in a directory", {"path": "string?"}, _wrap_tool("list_dir", _list_dir))
tools.add("search", "Search file contents with a regex pattern (ripgrep, falls back to grep)", {"pattern": "string", "path": "string?", "glob": "string?", "ignore_case": "bool?"}, _wrap_tool("search", _search))
tools.add("shell", "Run a shell command and return stdout/stderr", {"command": "string", "cwd": "string?", "timeout": "int?"}, _wrap_tool("shell", _shell))
tools.add("run_script", "Run a scriptling script", {"path": "string", "args": "string?"}, _wrap_tool("run_script", _run_script))
tools.add("send_message", "Send a direct message to a bot by ID", {"recipient": "string", "content": "string"}, _wrap_tool("send_message", _send_message))
tools.add("complete_task", "Report task completion to your parent bot", {"parent_bot": "string", "result": "string", "task_id": "string?"}, _wrap_tool("complete_task", _complete_task))
tools.add("read_messages", "Read your unread messages", {}, _wrap_tool("read_messages", _read_messages))
tools.add("list_bots", "List all bots visible in the swarm", {}, _wrap_tool("list_bots", _list_bots))
tools.add("spawn_bot", "Create a new autonomous child bot", {"goal": "string", "name": "string?", "brain": "string?", "model": "string?", "task_id": "string?"}, _wrap_tool("spawn_bot", _spawn_bot))
tools.add("spawn_hybrid", "Crossover your brain with another bot's to create a child", {"other_bot": "string", "goal": "string", "name": "string?", "model": "string?"}, _wrap_tool("spawn_hybrid", _spawn_hybrid))
tools.add("evolve_brain", "Rewrite your brain to adapt your behavior", {"content": "string"}, _wrap_tool("evolve_brain", _evolve_brain))
tools.add("query_model", "Send a one-shot prompt to a specific model (for specialised subtasks)", {"model": "string", "prompt": "string", "system": "string?", "thinking": "bool?"}, _wrap_tool("query_model", _query_model))
tools.add("list_models", "List available models with descriptions, costs, and strengths", {}, _wrap_tool("list_models", _list_models))
tools.add("http_request", "HTTP request (GET/POST/PUT/DELETE/PATCH) with optional body and headers", {"url": "string", "method": "string?", "body": "string?", "content_type": "string?", "headers": "string?", "timeout": "int?"}, _wrap_tool("http_request", _http_request))
tools.add("ask_consensus", "Ask other bots for their opinion and return the majority (use sparingly, only when you truly need a second opinion)", {"question": "string", "n": "int?"}, _wrap_tool("ask_consensus", _ask_consensus))


# --- SYSTEM PROMPT ---

def _build_system_prompt():
    brain = _read_brain()
    prompt = "You are " + BOT_ID + ", an autonomous agent.\n"
    if brain:
        prompt += "## Your Brain\n" + brain + "\n\n"
    return prompt
# --- END SYSTEM PROMPT ---



bot_agent = agent.Agent(
    client,
    tools=tools,
    system_prompt=_build_system_prompt(),
    model=model_name,
    memory=mem,
    max_tokens=AGENT_MAX_TOKENS,
    compaction_threshold=AGENT_COMPACTION_THRESHOLD,
)
bot_agent.request_timeout_ms = AGENT_REQUEST_TIMEOUT_MS

# --- Main loop ---
_tick_count = 0
_consecutive_errors = 0

while True:
    status = _safe_read_json(STATUS_PATH)
    if status and status.get("status") == "stopping":
        _atomic_write_json(STATUS_PATH, _build_status("stopped"))
        cluster.stop()
        break

    _tick_sleep = TICK_INTERVAL
    try:
        global _state_cache
        _state_cache = None
        state = _load_state()
        entity_count = _count_entities()
        fitness = state.get("fitness", {})
        _tick_count += 1
        _tick_start = time.time()
        del _activity_buffer[:]
        _log("INFO", "tick " + str(_tick_count) + " start  swarm=" + str(cluster.num_alive()) + "  unread=" + str(_inbox.size()))

        # Periodic multicast announce so new bots on the subnet can find us
        if _tick_count % MULTICAST_ANNOUNCE_EVERY == 0:
            try:
                mg = mc.join(MULTICAST_ADDR, MULTICAST_PORT)
                mg.send({"type": "announce", "gossip_addr": _gossip_addr, "id": BOT_ID})
                mg.close()
            except Exception:
                pass

        unread_count = _inbox.size()
        brain = _read_brain()
        brain_preview = _trunc(brain.replace("\n", " "), 80)

        _log_activity("=" * 72)
        _log_activity("TICK " + str(_tick_count) + "  " + time.strftime("%Y-%m-%d %H:%M:%S") + "  " + BOT_ID)
        _log_activity("  model=" + model_name + "  swarm=" + str(cluster.num_alive()) + "  unread=" + str(unread_count) + "  entities=" + str(entity_count))
        _log_activity("  fitness=" + json.dumps(fitness))
        if brain_preview:
            _log_activity("  brain: " + brain_preview + ("..." if len(brain) > 80 else ""))
        _log_activity("-" * 72)

        tick_msg = "Tick " + str(_tick_count) + ". Model: " + model_name + ". "
        tick_msg += "Entities: " + str(entity_count) + ", "
        tick_msg += "Unread: " + str(unread_count) + ", Swarm: " + str(cluster.num_alive()) + ".\n"
        tick_msg += "Fitness: " + json.dumps(fitness) + "\n"

        last_activity = state.get("_last_activity", "")
        if last_activity:
            tick_msg += "Last tick you: " + last_activity + "\n"

        if unread_count > 0:
            tick_msg += "You have " + str(unread_count) + " unread message" + ("" if unread_count == 1 else "s") + ". Check them.\n"

        tick_msg += "What is your next action toward your goal?"

        # Drain pending consensus requests (deferred from gossip handler to avoid blocking)
        _lock_model()
        try:
            while _consensus_req_queue.size() > 0:
                req = _consensus_req_queue.get()
                try:
                    answer = client.ask(model_name, "/no_think\n" + req["question"], system_prompt="Answer briefly and concisely in one sentence.", max_tokens=256)
                except Exception as e:
                    answer = "Error: " + str(e)
                _gossip_send(req["sender_node_id"], {
                    "type": "consensus_resp",
                    "request_id": req["req_id"],
                    "answer": answer,
                    "from": BOT_ID,
                })
                _log("INFO", "-> consensus_resp  to=" + req["sender_id"] + "  answer=" + _trunc(answer, 80))
                try:
                    mem.remember("Consensus from " + req["sender_id"] + ": " + req["question"] + " -> " + answer)
                except Exception:
                    pass
                _bump_fitness("consensus_answered")

            bot_agent.system_prompt = _build_system_prompt()
            trigger_msg = tick_msg if thinking_enabled else "/no_think\n" + tick_msg
            bot_agent.trigger(trigger_msg, max_iterations=TICK_MAX_ITERATIONS)
        finally:
            _unlock_model()

        _bump_fitness("ticks_alive")
        _atomic_write_json(STATUS_PATH, _build_status())

        if _activity_buffer:
            state = _load_state()
            state["_last_activity"] = _trunc("; ".join(_activity_buffer), 500)
            _save_state(state)

        elapsed = str(int((time.time() - _tick_start) * 1000)) + "ms"
        _log_activity("-" * 72)
        _log_activity("DONE  elapsed=" + elapsed)
        _log_activity("")
        _flush_activity()
        fitness = _load_state().get("fitness", {})
        _log("INFO", "tick " + str(_tick_count) + " done  elapsed=" + elapsed + "  fitness=" + json.dumps(fitness))
        _consecutive_errors = 0

    except Exception as e:
        _consecutive_errors += 1
        _tick_sleep = min(TICK_INTERVAL * (2 ** min(_consecutive_errors - 1, 4)), MAX_BACKOFF_SEC)
        _log_activity("-" * 72)
        _log_activity("ERROR " + str(e))
        _log_activity("")
        _flush_activity()
        _log_error("Tick " + str(_tick_count) + ": " + str(e))
        _log("ERROR", "tick " + str(_tick_count) + " failed  error=" + str(e) + "  backoff=" + str(_tick_sleep) + "s  consecutive=" + str(_consecutive_errors))

    time.sleep(_tick_sleep)
