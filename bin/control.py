#!/usr/bin/env scriptling

import os
import os.path
import json
import sys
import subprocess
import time
import scriptling.net.gossip as gossip

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_DIR = os.path.dirname(SCRIPT_DIR)
BOTS_DIR = os.path.join(PROJECT_DIR, "bots")
LIB_DIR = os.path.join(PROJECT_DIR, "lib")
TEMPLATE_PATH = os.path.join(LIB_DIR, "botcore.py")
DEFAULTS_PATH = os.path.join(LIB_DIR, "defaults.py")
PROMPT_PATH = os.path.join(LIB_DIR, "prompt.py")
STALE_THRESHOLD = 120
LOCKS_DIR = os.path.join(PROJECT_DIR, ".locks")

_SHELL_BLOCKED = ("curl", "wget")
LOCAL_IP = "0.0.0.0"
_BWRAP_AVAILABLE = False


def _ts():
    return time.strftime("%H:%M:%S")


def _log(level, msg):
    print(_ts() + " [" + level + "] " + msg)


def _detect_local_ip():
    try:
        result = subprocess.run(
            ["hostname", "-I"],
            capture_output=True,
            timeout=5,
        )
        if result.returncode == 0 and result.stdout:
            ip = str(result.stdout).strip().split()[0]
            if ip and ip != "0.0.0.0":
                return ip
    except Exception:
        pass
    return "0.0.0.0"


def _bot_allowed_paths(bot_id):
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    paths = [bot_dir, BOTS_DIR, LOCKS_DIR]
    bot_status = os.path.join(bot_dir, "status.json")
    if os.path.exists(bot_status):
        try:
            status = json.loads(os.read_file(bot_status))
            wp = status.get("workspace_path", "")
            if wp and os.path.exists(wp):
                paths.append(wp)
        except Exception:
            pass
    return ",".join(paths)


def _bot_spawn_cmd(bot_id, bot_script, log_path, append=False):
    redirect = ">>" if append else ">"
    return (
        "nohup scriptling --disable-lib=subprocess --allowed-paths="
        + _bot_allowed_paths(bot_id) + " "
        + bot_script + " " + redirect + " " + log_path + " 2>&1 &"
    )


def _load_env_defaults():
    env_file = os.path.join(PROJECT_DIR, ".env")
    if not os.path.exists(env_file):
        return
    content = os.read_file(env_file)
    for line in content.split("\n"):
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key and key not in os.environ:
            os.environ[key] = value


_load_env_defaults()
LOCAL_IP = _detect_local_ip()
os.environ["BOT_IP"] = LOCAL_IP
_BWRAP_AVAILABLE = subprocess.run(["which", "bwrap"], capture_output=True).returncode == 0

if "BOT_STALE_THRESHOLD" in os.environ:
    try:
        STALE_THRESHOLD = int(os.environ["BOT_STALE_THRESHOLD"])
    except Exception:
        pass


def _is_valid_name(name):
    if not name or len(name) > 64:
        return False
    for c in name:
        if not (c.isalnum() or c == "-" or c == "_"):
            return False
    return True


def _load_status(bot_id):
    path = os.path.join(BOTS_DIR, bot_id, "status.json")
    if not os.path.exists(path):
        return None
    try:
        return json.loads(os.read_file(path))
    except Exception:
        return None


def _save_status(bot_id, data):
    path = os.path.join(BOTS_DIR, bot_id, "status.json")
    tmp = path + ".tmp"
    os.write_file(tmp, json.dumps(data, indent=2))
    os.rename(tmp, path)


def _all_bots():
    if not os.path.exists(BOTS_DIR):
        return []
    bots = []
    for entry in os.listdir(BOTS_DIR):
        status = _load_status(entry)
        if status:
            bots.append(status)
    return bots


# NOTE: An identical copy of this function lives in botcore.py for child/self-spawning.
# Both copies must be kept in sync.
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


def _inject_block(source, start_marker, end_marker, content):
    start_idx = source.find(start_marker)
    end_idx = source.find(end_marker)
    if start_idx < 0 or end_idx < 0:
        return None
    return (
        source[:start_idx]
        + start_marker + "\n"
        + content + "\n"
        + end_marker + "\n"
        + source[end_idx + len(end_marker):]
    )


def _load_workspaces():
    path = os.path.join(PROJECT_DIR, "workspaces.json")
    if os.path.exists(path):
        try:
            return json.loads(os.read_file(path))
        except Exception:
            pass
    return {}


def _ws_path(workspaces, name):
    ws = workspaces.get(name, "")
    if isinstance(ws, dict):
        return ws.get("path", "")
    return ws if isinstance(ws, str) else ""


def _ws_config(workspaces, name):
    ws = workspaces.get(name)
    if isinstance(ws, dict):
        return ws
    if isinstance(ws, str):
        return {"path": ws}
    return {}


def _build_bwrap_cmd(command, bot_dir, cwd, workspace_path=""):
    if os.environ.get("BOT_SHELL_SANDBOX", "true").lower() == "false":
        return None
    if not _BWRAP_AVAILABLE:
        return None

    try:
        rel = os.path.relpath(cwd, bot_dir)
        inner_cwd = "/" if rel.startswith("..") else ("/" + rel).rstrip("/") or "/"
    except Exception:
        inner_cwd = "/"

    cmd = ["bwrap", "--chdir", inner_cwd, "--bind", bot_dir, "/"]

    for sysdir in ("/usr", "/bin", "/sbin", "/lib", "/lib64", "/lib32", "/etc", "/tmp"):
        if not os.path.exists(sysdir):
            continue
        try:
            rlink = subprocess.run(["readlink", sysdir], capture_output=True, timeout=5)
            if rlink.returncode == 0:
                cmd += ["--symlink", str(rlink.stdout).strip(), sysdir]
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

    if workspace_path and os.path.exists(workspace_path):
        cmd += ["--bind", workspace_path, workspace_path]

    cmd += ["--", "bash", "-c", command]
    return cmd


def cmd_spawn(name, goal, opts):
    if not _is_valid_name(name):
        print("Invalid name. Use only letters, digits, dash, underscore (max 64 chars).")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, name)
    if os.path.exists(bot_dir):
        print("Bot already exists: " + name)
        sys.exit(1)

    model = opts.get("model", os.environ.get("BOT_MODEL", ""))
    brain = opts.get("brain", "") or "I am " + name + ". I was just created. I need to explore and understand my purpose.\n"
    seeds = [s.strip() for s in opts["seeds"].split(",") if s.strip()] if opts.get("seeds") else []
    thinking = opts.get("thinking", "true").lower() != "false"
    workspace_name = opts.get("workspace", "")
    scope = opts.get("scope", "")
    allowed_workspaces = [s.strip() for s in opts["allowed_workspaces"].split(",") if s.strip()] if opts.get("allowed_workspaces") else []
    config = {
        "name": name,
        "goal": goal,
        "model": model,
        "brain": brain,
        "seed_addrs": seeds,
        "thinking": thinking,
    }

    if not os.path.exists(BOTS_DIR):
        os.makedirs(BOTS_DIR)

    template = os.read_file(TEMPLATE_PATH)
    source = _inject_config(template, config)
    if source is None:
        _log("ERR", "bot template is missing CONFIG markers")
        sys.exit(1)
    source = _inject_block(source, "# --- DEFAULTS ---", "# --- END DEFAULTS ---", os.read_file(DEFAULTS_PATH))
    if source is None:
        _log("ERR", "bot template is missing DEFAULTS markers")
        sys.exit(1)
    source = _inject_block(source, "# --- SYSTEM PROMPT ---", "# --- END SYSTEM PROMPT ---", os.read_file(PROMPT_PATH))
    if source is None:
        _log("ERR", "bot template is missing SYSTEM PROMPT markers")
        sys.exit(1)

    models_path = os.path.join(PROJECT_DIR, "models.json")
    if os.path.exists(models_path):
        try:
            models_json = os.read_file(models_path)
            json.loads(models_json)  # validate
            models_block = "AVAILABLE_MODELS = " + models_json
        except Exception:
            models_block = "AVAILABLE_MODELS = []"
    else:
        models_block = "AVAILABLE_MODELS = []"
    source = _inject_block(source, "# --- MODELS ---", "# --- END MODELS ---", models_block)
    if source is None:
        _log("ERR", "bot template is missing MODELS markers")
        sys.exit(1)

    os.makedirs(bot_dir)
    os.write_file(os.path.join(bot_dir, "bot.py"), source)

    entities_dir = os.path.join(bot_dir, "entities")
    os.makedirs(entities_dir)
    ref_src = os.path.join(LIB_DIR, "scriptling-reference.md")
    if os.path.exists(ref_src):
        os.write_file(os.path.join(entities_dir, "scriptling-reference.md"), os.read_file(ref_src))

    workspaces = _load_workspaces()
    status = {
        "id": name,
        "goal": goal,
        "status": "created",
        "created_at": int(time.time()),
        "gossip_addr": "",
        "fitness": {},
    }
    if workspace_name and workspace_name in workspaces:
        ws_cfg = _ws_config(workspaces, workspace_name)
        ws_path_val = ws_cfg.get("path", "")
        if ws_path_val and os.path.exists(ws_path_val):
            status["workspace"] = workspace_name
            status["workspace_path"] = ws_path_val
            ws_secret = ws_cfg.get("gossip_secret", "")
            if ws_secret:
                status["gossip_secret"] = ws_secret
            effective_scope = scope or ws_cfg.get("default_scope", "isolated")
        else:
            effective_scope = scope or "open"
    else:
        effective_scope = scope or "open"
    if effective_scope != "open":
        status["scope"] = effective_scope
    if allowed_workspaces:
        status["allowed_workspaces"] = allowed_workspaces

    tmp = os.path.join(bot_dir, "status.json.tmp")
    os.write_file(tmp, json.dumps(status, indent=2))
    os.rename(tmp, os.path.join(bot_dir, "status.json"))

    _log("OK", "spawned " + name)
    print("  dir:   " + bot_dir)
    print("  goal:  " + goal)
    print("  model: " + (model or "(default)"))
    print("  scope: " + effective_scope)
    if workspace_name:
        print("  workspace: " + workspace_name)
    if allowed_workspaces:
        print("  allowed_workspaces: " + ", ".join(allowed_workspaces))
    print("  start: scriptling bin/control.py start " + name)


def cmd_list():
    bots = _all_bots()
    if not bots:
        print("No bots found.")
        return
    now = int(time.time())
    name_w = max(len(b.get("id", "")) for b in bots)
    name_w = max(name_w, 4)
    for b in sorted(bots, key=lambda x: x.get("id", "")):
        bid = b.get("id", "?")
        fitness = b.get("fitness", {})
        ticks = fitness.get("ticks_alive", 0)
        spawns = fitness.get("spawns", 0)
        addr = b.get("gossip_addr", "")
        status_str = b.get("status", "?")
        last_tick = b.get("last_tick_ts", 0)
        if status_str == "running" and last_tick and (now - last_tick) > STALE_THRESHOLD:
            status_str = "STALE"
        line = bid.ljust(name_w) + "  " + status_str.upper().ljust(8)
        line += "  ticks=" + str(ticks) + "  spawns=" + str(spawns)
        if addr:
            line += "  @ " + addr
        print(line)
        print("  " + b.get("goal", "?"))
        if b.get("is_leader"):
            print("  ** leader **")


def cmd_start(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    bot_script = os.path.join(bot_dir, "bot.py")
    if not os.path.exists(bot_script):
        print("Bot not found: " + bot_id)
        sys.exit(1)
    status = _load_status(bot_id) or {}
    current = status.get("status", "")
    if current == "running":
        _log("WARN", bot_id + " already running")
        return
    log_path = os.path.join(bot_dir, "output.log")
    subprocess.run(_bot_spawn_cmd(bot_id, bot_script, log_path), shell=True)
    _log("OK", "started " + bot_id)


def cmd_stop(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    status = _load_status(bot_id)
    if status is None:
        print("Bot not found: " + bot_id)
        sys.exit(1)
    status["status"] = "stopping"
    _save_status(bot_id, status)
    _log("OK", "stop signal sent to " + bot_id + " (exits on next tick)")


def cmd_kill(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    status = _load_status(bot_id)
    if status is None:
        print("Bot not found: " + bot_id)
        sys.exit(1)
    bot_script = os.path.join(BOTS_DIR, bot_id, "bot.py")
    try:
        subprocess.run(["pkill", "-f", bot_script])
    except Exception:
        pass
    status["status"] = "killed"
    _save_status(bot_id, status)
    _log("OK", "killed " + bot_id)


def cmd_send(bot_id, message):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    status = _load_status(bot_id)
    if status is None:
        print("Bot not found: " + bot_id)
        sys.exit(1)
    gossip_addr = status.get("gossip_addr", "")
    if not gossip_addr or gossip_addr == "0.0.0.0:0":
        print("Bot has no gossip address (not running?): " + bot_id)
        sys.exit(1)
    try:
        c = gossip.create(bind_addr="0.0.0.0:0")
        c.start()
        c.join([gossip_addr])
        target = None
        for n in c.alive_nodes():
            if n.get("metadata", {}).get("id") == bot_id:
                target = n
                break
        if target is None:
            print("Bot not reachable at " + gossip_addr)
            c.stop()
            sys.exit(1)
        payload = {"type": "message", "from": "operator", "content": message}
        secret = os.environ.get("BOT_GOSSIP_SECRET", "")
        if secret:
            payload["_secret"] = secret
        c.send_to(target["id"], gossip.MSG_USER, payload)
        c.stop()
        _log("OK", "sent to " + bot_id + ": " + message)
    except Exception as e:
        _log("ERR", str(e))
        sys.exit(1)


def cmd_tail(bot_id, log_name="bot.log"):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    valid_logs = ("bot.log", "output.log")
    if log_name not in valid_logs:
        print("Unknown log. Choose: " + ", ".join(valid_logs))
        sys.exit(1)
    log_path = os.path.join(BOTS_DIR, bot_id, log_name)
    if not os.path.exists(log_path):
        print("Log not found: " + log_path)
        sys.exit(1)
    r = subprocess.run(["tail", "-n", "20", log_path], capture_output=True)
    if r.stdout:
        print(str(r.stdout), end="")

    r2 = subprocess.run(["wc", "-c", log_path], capture_output=True)
    offset = 0
    if r2.returncode == 0 and r2.stdout:
        try:
            offset = int(str(r2.stdout).strip().split()[0])
        except Exception:
            pass

    print("[following " + log_path + " -- Ctrl+C to stop]")
    while True:
        try:
            r3 = subprocess.run(
                ["tail", "-c", "+" + str(offset + 1), log_path],
                capture_output=True,
            )
            if r3.returncode == 0 and r3.stdout:
                chunk = str(r3.stdout)
                print(chunk, end="")
                offset += len(chunk)
            time.sleep(0.5)
        except KeyboardInterrupt:
            print("")
            break


def cmd_logs(bot_id, lines=40):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    for log_name in ("bot.log", "output.log"):
        log_path = os.path.join(bot_dir, log_name)
        print("--- " + log_name + " (last " + str(lines) + " lines) ---")
        if os.path.exists(log_path):
            result = subprocess.run(
                ["tail", "-n", str(lines), log_path],
                capture_output=True,
            )
            if result.stdout:
                print(result.stdout)
        else:
            print("(empty)")


def cmd_start_all():
    if not os.path.exists(BOTS_DIR):
        print("No bots directory.")
        return
    started = 0
    skipped = 0
    for entry in sorted(os.listdir(BOTS_DIR)):
        status = _load_status(entry)
        if status is None:
            continue
        if status.get("status") == "running":
            skipped += 1
            continue
        bot_script = os.path.join(BOTS_DIR, entry, "bot.py")
        if not os.path.exists(bot_script):
            continue
        log_path = os.path.join(BOTS_DIR, entry, "output.log")
        subprocess.run(_bot_spawn_cmd(entry, bot_script, log_path), shell=True)
        _log("OK", "started " + entry)
        started += 1
    print("done. started=" + str(started) + " skipped=" + str(skipped))


def cmd_stop_all():
    bots = _all_bots()
    if not bots:
        print("No bots found.")
        return
    stopped = 0
    for b in bots:
        if b.get("status") == "running":
            b["status"] = "stopping"
            _save_status(b["id"], b)
            _log("OK", "stop signal -> " + b["id"])
            stopped += 1
    print("done. stop signals sent: " + str(stopped))


def cmd_kill_all():
    bots = _all_bots()
    if not bots:
        print("No bots found.")
        return
    killed = 0
    for b in bots:
        bot_script = os.path.join(BOTS_DIR, b["id"], "bot.py")
        try:
            subprocess.run(["pkill", "-f", bot_script])
        except Exception:
            pass
        b["status"] = "killed"
        _save_status(b["id"], b)
        _log("OK", "killed " + b["id"])
        killed += 1
    print("done. killed=" + str(killed))


def cmd_restart(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    if not os.path.exists(bot_dir):
        print("Bot not found: " + bot_id)
        sys.exit(1)
    bot_script = os.path.join(bot_dir, "bot.py")
    try:
        subprocess.run(["pkill", "-f", bot_script])
    except Exception:
        pass
    status = _load_status(bot_id) or {}
    status["status"] = "killed"
    _save_status(bot_id, status)
    time.sleep(2)
    log_path = os.path.join(bot_dir, "output.log")
    subprocess.run(_bot_spawn_cmd(bot_id, bot_script, log_path), shell=True)
    _log("OK", "restarted " + bot_id)


def cmd_remove(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    if not os.path.exists(bot_dir):
        print("Bot not found: " + bot_id)
        sys.exit(1)
    bot_script = os.path.join(bot_dir, "bot.py")
    try:
        subprocess.run(["pkill", "-f", bot_script])
    except Exception:
        pass
    subprocess.run(["rm", "-rf", bot_dir])
    _log("OK", "removed " + bot_id)


def cmd_status():
    bots = _all_bots()
    running = [b for b in bots if b.get("status") == "running" and b.get("gossip_addr")]
    if not running:
        print("No running bots with gossip addresses found.")
        return
    target = running[0]
    gossip_addr = target["gossip_addr"]
    try:
        c = gossip.create(bind_addr="0.0.0.0:0")
        c.start()
        c.join([gossip_addr])
        time.sleep(1)
        nodes = c.alive_nodes()
        if not nodes:
            print("No peers reachable via " + gossip_addr)
            c.stop()
            return
        print("Swarm via " + target["id"] + " (" + gossip_addr + ")")
        print("")
        name_w = max(len(n.get("metadata", {}).get("id", n["id"][:16])) for n in nodes)
        name_w = max(name_w, 4)
        for n in nodes:
            meta = n.get("metadata", {})
            nid = meta.get("id", n["id"][:16])
            addr = meta.get("gossip_addr", "")
            goal = meta.get("goal", "")
            role = meta.get("role", "")
            local = _load_status(nid) if _is_valid_name(nid) else None
            line = "  " + nid.ljust(name_w)
            if role:
                line += "  [" + role + "]"
            line += "  @ " + addr
            if local:
                f = local.get("fitness", {})
                fparts = []
                if f.get("ticks_alive"):
                    fparts.append("ticks=" + str(f["ticks_alive"]))
                if f.get("spawns"):
                    fparts.append("spawns=" + str(f["spawns"]))
                if f.get("brain_evolutions"):
                    fparts.append("brain=" + str(f["brain_evolutions"]))
                if fparts:
                    line += "  " + " ".join(fparts)
            print(line)
            if goal:
                print("    " + goal[:80])
        print("")
        print(str(len(nodes)) + " nodes")
        c.stop()
    except Exception as e:
        _log("ERR", str(e))
        sys.exit(1)


def cmd_export(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    if not os.path.exists(bot_dir):
        print("Bot not found: " + bot_id)
        sys.exit(1)

    archive_name = bot_id + "-" + str(int(time.time())) + ".tar.gz"
    archive_path = os.path.join(PROJECT_DIR, archive_name)
    staging = archive_name.replace(".tar.gz", "")
    staging_dir = os.path.join(PROJECT_DIR, staging)

    try:
        os.makedirs(staging_dir)
        os.makedirs(os.path.join(staging_dir, "bots"))
        os.makedirs(os.path.join(staging_dir, "bin"))

        dest_bot_dir = os.path.join(staging_dir, "bots", bot_id)
        subprocess.run(["cp", "-r", bot_dir, dest_bot_dir])
        for runtime_file in ("memory.db", "output.log", "bot.log"):
            p = os.path.join(dest_bot_dir, runtime_file)
            if os.path.exists(p):
                subprocess.run(["rm", "-rf", p])

        subprocess.run(["cp", os.path.join(SCRIPT_DIR, "control.py"), os.path.join(staging_dir, "bin", "control.py")])
        subprocess.run(["cp", "-r", LIB_DIR, os.path.join(staging_dir, "lib")])

        env_example = os.path.join(PROJECT_DIR, ".env.example")
        if os.path.exists(env_example):
            subprocess.run(["cp", env_example, os.path.join(staging_dir, ".env.example")])

        _export_ws_path = (_load_status(bot_id) or {}).get("workspace_path", "")
        _export_allowed = "$PROJ_DIR/bots/" + bot_id + ",$PROJ_DIR/bots,$PROJ_DIR/.locks"
        if _export_ws_path:
            _export_allowed += "," + _export_ws_path
        bootstrap = (
            "#!/bin/bash\nset -e\n"
            "PROJ_DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
            "if [ ! -f \"$PROJ_DIR/.env\" ]; then\n"
            "  echo \"No .env found. Copy .env.example to .env and fill in your credentials.\"\n"
            "  exit 1\n"
            "fi\n"
            "BOT_IP=$(hostname -I 2>/dev/null | awk '{print $1}')\n"
            "export BOT_IP=${BOT_IP:-0.0.0.0}\n"
            "nohup scriptling --disable-lib=subprocess --allowed-paths=\"" + _export_allowed + "\" "
            "\"$PROJ_DIR/bots/" + bot_id + "/bot.py\" "
            "> \"$PROJ_DIR/bots/" + bot_id + "/output.log\" 2>&1 &\n"
            "echo \"Started " + bot_id + " (PID $!)\"\n"
            "echo \"Manage with: scriptling bin/control.py list\"\n"
        )
        os.write_file(os.path.join(staging_dir, "bootstrap.sh"), bootstrap)
        subprocess.run(["chmod", "+x", os.path.join(staging_dir, "bootstrap.sh")])

        result = subprocess.run(
            ["tar", "czf", archive_path, "-C", PROJECT_DIR, staging],
            capture_output=True,
        )
        if result.returncode != 0:
            _log("ERR", "export failed: " + str(result.stderr))
            sys.exit(1)
    finally:
        subprocess.run(["rm", "-rf", staging_dir])

    _log("OK", "exported " + archive_path)
    print("  tar xzf " + archive_name + " && cd " + staging + " && cp .env.example .env")
    print("  # edit .env, then: bash bootstrap.sh")


def cmd_restart_stale():
    bots = _all_bots()
    now = int(time.time())
    stale = []
    for b in bots:
        if b.get("status") == "running":
            last_tick = b.get("last_tick_ts", 0)
            if last_tick and (now - last_tick) > STALE_THRESHOLD:
                stale.append(b["id"])
    if not stale:
        print("No stale bots found.")
        return
    for bot_id in stale:
        _log("WARN", "stale bot: " + bot_id)
        cmd_restart(bot_id)
    print("done. restarted " + str(len(stale)) + " stale bots")


def cmd_watchdog():
    interval = 30

    _wd_port = 37000
    _wd_port_env = os.environ.get("BOT_WATCHDOG_PORT", "")
    if _wd_port_env:
        try:
            _wd_port = int(_wd_port_env)
        except Exception:
            pass

    proxy = gossip.create(bind_addr="0.0.0.0:" + str(_wd_port))
    proxy.start()
    _wd_addr = LOCAL_IP + ":" + str(_wd_port)
    proxy.set_metadata("role", "watchdog")
    proxy.set_metadata("id", "watchdog-" + str(int(time.time())))
    proxy.set_metadata("gossip_addr", _wd_addr)

    bots = _all_bots()
    for b in bots:
        addr = b.get("gossip_addr", "")
        if addr and b.get("status") == "running":
            try:
                proxy.join([addr])
                break
            except Exception:
                continue

    secret = os.environ.get("BOT_GOSSIP_SECRET", "")
    allowlist = [h.strip() for h in os.environ.get("BOT_SHELL_ALLOWLIST", "").split(",") if h.strip()]
    workspaces = _load_workspaces()

    _all_secrets = set()
    if secret:
        _all_secrets.add(secret)
    for _ws_name, _ws_cfg in workspaces.items():
        if isinstance(_ws_cfg, dict) and _ws_cfg.get("gossip_secret"):
            _all_secrets.add(_ws_cfg["gossip_secret"])

    def _get_ws_secret(ws_name):
        ws_cfg = workspaces.get(ws_name, {})
        if isinstance(ws_cfg, dict):
            return ws_cfg.get("gossip_secret", "")
        return ""

    def _handle_shell(payload):
        provided_secret = payload.get("_secret", "")
        if _all_secrets and provided_secret not in _all_secrets:
            return {"exit_code": 1, "stderr": "Unauthorized"}

        bot_id = payload.get("bot_id", "")
        command = payload.get("command", "")
        timeout = int(payload.get("timeout", 30))
        cwd = payload.get("cwd", "")

        if not bot_id or not command:
            return {"exit_code": 1, "stderr": "Missing bot_id or command"}

        if not _is_valid_name(bot_id):
            return {"exit_code": 1, "stderr": "Invalid bot_id"}

        bot_dir = os.path.join(BOTS_DIR, bot_id)
        if not os.path.exists(bot_dir):
            return {"exit_code": 1, "stderr": "Bot directory not found"}

        stripped = command.strip().lstrip("/ \t")
        first_word = stripped.split()[0].split("/")[-1] if stripped.split() else ""

        if first_word in _SHELL_BLOCKED:
            return {"exit_code": 1, "stderr": first_word + " is not allowed. Use http_request tool instead."}

        if allowlist and first_word not in allowlist:
            return {"exit_code": 1, "stderr": first_word + " is not in the shell allowlist (" + ", ".join(allowlist) + ")."}

        if cwd:
            parts = cwd.replace("\\", "/").split("/")
            if ".." in parts or cwd.startswith("/"):
                cwd = ""
        real_cwd = os.path.join(bot_dir, cwd) if cwd else bot_dir

        workspace_path = ""
        bot_status_path = os.path.join(bot_dir, "status.json")
        try:
            bot_status = json.loads(os.read_file(bot_status_path))
            workspace_path = bot_status.get("workspace_path", "")
        except Exception:
            pass

        bwrap_cmd = _build_bwrap_cmd(command, bot_dir, real_cwd, workspace_path)
        sandboxed = bwrap_cmd is not None
        _log("PROXY", bot_id + " cmd=" + _trunc(command, 60) + (" [bwrap]" if sandboxed else " [raw]"))

        try:
            if bwrap_cmd:
                result = subprocess.run(bwrap_cmd, capture_output=True, timeout=timeout)
            else:
                result = subprocess.run(command, shell=True, capture_output=True, timeout=timeout, cwd=real_cwd)
            output = {"exit_code": result.returncode}
            if result.stdout:
                output["stdout"] = str(result.stdout)[:50000]
            if result.stderr:
                output["stderr"] = str(result.stderr)[:10000]
            return output
        except subprocess.TimeoutExpired:
            return {"exit_code": 1, "stderr": "Command timed out after " + str(timeout) + "s"}
        except Exception as e:
            return {"exit_code": 1, "stderr": str(e)}

    def _handle_relay(payload):
        provided_secret = payload.get("_secret", "")
        if _all_secrets and provided_secret not in _all_secrets:
            return {"error": "Unauthorized"}

        from_bot = payload.get("from", "")
        target_bot = payload.get("target_bot", "")
        content = payload.get("content", "")
        if not from_bot or not target_bot or not content:
            return {"error": "Missing from, target_bot, or content"}

        if not _is_valid_name(from_bot):
            return {"error": "Invalid from bot ID"}
        if not _is_valid_name(target_bot):
            return {"error": "Invalid target bot ID"}

        bot_status = _load_status(from_bot)
        if not bot_status:
            return {"error": "Unknown bot: " + from_bot}

        bot_scope = bot_status.get("scope", "open")
        if bot_scope != "gateway":
            return {"error": "Bot is not gateway scope"}

        allowed = bot_status.get("allowed_workspaces", [])

        target_status = _load_status(target_bot)
        if not target_status:
            return {"error": "Target not found: " + target_bot}

        target_ws = target_status.get("workspace", "")
        if not target_ws:
            return {"error": "Target bot has no workspace"}
        if target_ws not in allowed:
            return {"error": "Workspace '" + target_ws + "' not in allowed list"}

        target_node = None
        for n in proxy.alive_nodes():
            if n.get("metadata", {}).get("id") == target_bot:
                target_node = n
                break

        if target_node is None:
            return {"error": "Target bot not online: " + target_bot}

        relay_payload = {
            "type": "relayed_message",
            "from": from_bot,
            "content": content,
            "relayed_by": "watchdog",
        }
        ws_secret = _get_ws_secret(target_ws)
        if ws_secret:
            relay_payload["_secret"] = ws_secret
        elif secret:
            relay_payload["_secret"] = secret
        proxy.send_to(target_node["id"], gossip.MSG_USER, relay_payload)
        _log("RELAY", from_bot + " -> " + target_bot + " ws=" + target_ws)
        return {"status": "relayed", "target": target_bot}

    def _on_request(msg):
        payload = msg.get("payload", {})
        if not isinstance(payload, dict):
            return None
        msg_type = payload.get("type", "")
        if msg_type == "shell_req":
            return _handle_shell(payload)
        if msg_type == "relay_req":
            return _handle_relay(payload)
        return None

    proxy.handle_with_reply(gossip.MSG_USER, _on_request)

    sandbox_str = "bwrap" if _BWRAP_AVAILABLE else "unsandboxed"
    _log("START", "watchdog + command proxy [" + sandbox_str + "] interval=" + str(interval) + "s")
    _log("INFO", "local ip: " + LOCAL_IP)
    _log("INFO", "swarm members: " + str(proxy.num_alive()))

    try:
        while True:
            try:
                if proxy.num_alive() <= 1:
                    bots = _all_bots()
                    for b in bots:
                        addr = b.get("gossip_addr", "")
                        if addr and b.get("status") == "running" and addr != "0.0.0.0:0":
                            try:
                                proxy.join([addr])
                                _log("JOIN", "joined cluster via " + b["id"] + " @ " + addr)
                                break
                            except Exception:
                                continue

                bots = _all_bots()
                for b in bots:
                    bot_id = b["id"]
                    bot_script = os.path.join(BOTS_DIR, bot_id, "bot.py")

                    if b.get("status") == "created":
                        if os.path.exists(bot_script):
                            if not b.get("workspace_path"):
                                parent_id = b.get("parent", "")
                                if parent_id:
                                    parent_status_path = os.path.join(BOTS_DIR, parent_id, "status.json")
                                    try:
                                        parent_status = json.loads(os.read_file(parent_status_path))
                                        wp = parent_status.get("workspace_path", "")
                                        if wp:
                                            b["workspace_path"] = wp
                                            b["workspace"] = parent_status.get("workspace", "")
                                        if not b.get("gossip_secret"):
                                            gs = parent_status.get("gossip_secret", "")
                                            if gs:
                                                b["gossip_secret"] = gs
                                        if not b.get("scope"):
                                            ps = parent_status.get("scope", "")
                                            if ps:
                                                b["scope"] = ps
                                        if not b.get("allowed_workspaces"):
                                            paw = parent_status.get("allowed_workspaces", [])
                                            if paw:
                                                b["allowed_workspaces"] = paw
                                    except Exception:
                                        pass
                            b["status"] = "starting"
                            _save_status(bot_id, b)
                            log_path = os.path.join(BOTS_DIR, bot_id, "output.log")
                            subprocess.run(_bot_spawn_cmd(bot_id, bot_script, log_path), shell=True)
                            _log("SPAWN", bot_id)
                        continue

                    if b.get("status") not in ("running",):
                        continue
                    check = subprocess.run(
                        ["pgrep", "-f", bot_script],
                        capture_output=True,
                    )
                    if check.returncode != 0:
                        _log("CRASH", bot_id + " -- restarting")
                        log_path = os.path.join(BOTS_DIR, bot_id, "output.log")
                        subprocess.run(_bot_spawn_cmd(bot_id, bot_script, log_path, append=True), shell=True)
                time.sleep(interval)
            except Exception as e:
                _log("ERR", str(e))
                time.sleep(interval)
    except KeyboardInterrupt:
        _log("STOP", "watchdog")
    finally:
        proxy.stop()


def _trunc(s, n):
    return s[:n] + "..." if len(s) > n else s


def main():
    args = sys.argv
    if len(args) < 2:
        print("Usage: control <command> [options]")
        print("")
        print("Commands:")
        print("  spawn <name> <goal> [k=v ...]  Create a bot")
        print("    Options: base_url=  model=  brain=  seeds=  thinking=false")
        print("             workspace=  scope=open|isolated|gateway|family  allowed_workspaces=ws1,ws2")
        print("  list                  List all bots (flags STALE after " + str(STALE_THRESHOLD) + "s)")
        print("  status                Live swarm view via gossip")
        print("  start <bot>           Start a bot")
        print("  start-all             Start all stopped bots")
        print("  stop <bot>            Graceful stop (next tick)")
        print("  stop-all              Graceful stop all running bots")
        print("  kill <bot>            Immediate SIGTERM")
        print("  kill-all              Immediate SIGTERM all bots")
        print("  restart <bot>         Kill + start")
        print("  restart-stale         Restart all bots flagged STALE")
        print("  export <bot>          Package bot + tools into a portable archive")
        print("  remove <bot>          Kill + delete bot directory")
        print("  logs <bot> [N]        Last N lines of bot.log + output.log")
        print("  tail <bot> [log]      Follow log in real time (default: bot.log)")
        print("  send <bot> <msg>      Send a message to a running bot")
        print("  watchdog              Auto-restart crashed bots + command proxy (runs until Ctrl+C)")
        sys.exit(1)

    command = args[1]
    if command == "spawn":
        if len(args) < 4:
            print("Usage: control spawn <name> <goal> [key=value ...]")
            sys.exit(1)
        opts = {}
        for a in args[4:]:
            if "=" in a:
                k, v = a.split("=", 1)
                opts[k.strip()] = v.strip()
        cmd_spawn(args[2], args[3], opts)
    elif command == "list":
        cmd_list()
    elif command == "status":
        cmd_status()
    elif command == "start-all":
        cmd_start_all()
    elif command == "stop-all":
        cmd_stop_all()
    elif command == "kill-all":
        cmd_kill_all()
    elif command == "watchdog":
        cmd_watchdog()
    elif command == "start":
        if len(args) < 3:
            print("Usage: control start <bot-id>")
            sys.exit(1)
        cmd_start(args[2])
    elif command == "stop":
        if len(args) < 3:
            print("Usage: control stop <bot-id>")
            sys.exit(1)
        cmd_stop(args[2])
    elif command == "kill":
        if len(args) < 3:
            print("Usage: control kill <bot-id>")
            sys.exit(1)
        cmd_kill(args[2])
    elif command == "restart":
        if len(args) < 3:
            print("Usage: control restart <bot-id>")
            sys.exit(1)
        cmd_restart(args[2])
    elif command == "restart-stale":
        cmd_restart_stale()
    elif command == "export":
        if len(args) < 3:
            print("Usage: control export <bot-id>")
            sys.exit(1)
        cmd_export(args[2])
    elif command == "remove":
        if len(args) < 3:
            print("Usage: control remove <bot-id>")
            sys.exit(1)
        cmd_remove(args[2])
    elif command == "logs":
        if len(args) < 3:
            print("Usage: control logs <bot-id> [lines]")
            sys.exit(1)
        lines = int(args[3]) if len(args) > 3 else 40
        cmd_logs(args[2], lines)
    elif command == "tail":
        if len(args) < 3:
            print("Usage: control tail <bot-id> [bot|output]")
            sys.exit(1)
        log_name = args[3] + ".log" if len(args) > 3 else "bot.log"
        cmd_tail(args[2], log_name)
    elif command == "send":
        if len(args) < 4:
            print("Usage: control send <bot-id> <message>")
            sys.exit(1)
        cmd_send(args[2], args[3])
    else:
        print("Unknown command: " + command)
        sys.exit(1)


main()
