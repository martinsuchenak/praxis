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
        + end_marker
        + source[end_idx + len(end_marker):]
    )


def cmd_spawn(name, goal, opts):
    if not _is_valid_name(name):
        print("Invalid name. Use only letters, digits, dash, underscore (max 64 chars).")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, name)
    if os.path.exists(bot_dir):
        print("Bot already exists: " + name)
        sys.exit(1)

    base_url = opts.get("base_url", os.environ.get("BOT_BASE_URL", ""))
    model = opts.get("model", os.environ.get("BOT_MODEL", ""))
    brain = opts.get("brain", "") or "I am " + name + ". I was just created. I need to explore and understand my purpose.\n"
    seeds = [s.strip() for s in opts["seeds"].split(",") if s.strip()] if opts.get("seeds") else []
    thinking = opts.get("thinking", "true").lower() != "false"

    config = {
        "name": name,
        "goal": goal,
        "api_key": "",
        "base_url": base_url,
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
        print("Error: bot template is missing CONFIG markers.")
        sys.exit(1)
    source = _inject_block(source, "# --- DEFAULTS ---", "# --- END DEFAULTS ---", os.read_file(DEFAULTS_PATH))
    if source is None:
        print("Error: bot template is missing DEFAULTS markers.")
        sys.exit(1)
    source = _inject_block(source, "# --- SYSTEM PROMPT ---", "# --- END SYSTEM PROMPT ---", os.read_file(PROMPT_PATH))
    if source is None:
        print("Error: bot template is missing SYSTEM PROMPT markers.")
        sys.exit(1)

    os.makedirs(bot_dir)
    os.write_file(os.path.join(bot_dir, "bot.py"), source)

    tmp = os.path.join(bot_dir, "status.json.tmp")
    os.write_file(tmp, json.dumps({
        "id": name,
        "goal": goal,
        "status": "created",
        "created_at": int(time.time()),
        "gossip_addr": "",
        "fitness": {},
    }, indent=2))
    os.rename(tmp, os.path.join(bot_dir, "status.json"))

    print("Created: " + name)
    print("Dir:     " + bot_dir)
    print("Start:   scriptling bin/control.py start " + name)


def cmd_list():
    bots = _all_bots()
    if not bots:
        print("No bots found.")
        return
    now = int(time.time())
    for b in sorted(bots, key=lambda x: x.get("id", "")):
        fitness = b.get("fitness", {})
        ticks = fitness.get("ticks_alive", 0)
        spawns = fitness.get("spawns", 0)
        gossip = b.get("gossip_addr", "")
        addr_str = "  @ " + gossip if gossip else ""
        status_str = b.get("status", "?")
        last_tick = b.get("last_tick_ts", 0)
        if status_str == "running" and last_tick and (now - last_tick) > STALE_THRESHOLD:
            status_str = "STALE"
        print(
            b.get("id", "?")
            + " | " + status_str
            + " | ticks=" + str(ticks) + " spawns=" + str(spawns)
            + addr_str
            + "\n  goal: " + b.get("goal", "?")
        )


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
        print("Bot already running: " + bot_id)
        sys.exit(1)
    log_path = os.path.join(bot_dir, "output.log")
    subprocess.run(
        "nohup scriptling " + bot_script + " > " + log_path + " 2>&1 &",
        shell=True,
    )
    print("Started: " + bot_id)


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
    print("Stop signal sent to: " + bot_id + " (will exit on next tick)")


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
    print("Killed: " + bot_id)


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
            print("Bot is not reachable at " + gossip_addr)
            c.stop()
            sys.exit(1)
        payload = {"type": "message", "from": "operator", "content": message}
        secret = os.environ.get("BOT_GOSSIP_SECRET", "")
        if secret:
            payload["_secret"] = secret
        c.send_to(target["id"], gossip.MSG_USER, payload)
        c.stop()
        print("Sent to " + bot_id + ": " + message)
    except Exception as e:
        print("Error: " + str(e))
        sys.exit(1)


def cmd_tail(bot_id, log_name="output.log"):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    valid_logs = ("output.log", "activity.log", "errors.log")
    if log_name not in valid_logs:
        print("Unknown log. Choose: " + ", ".join(valid_logs))
        sys.exit(1)
    log_path = os.path.join(BOTS_DIR, bot_id, log_name)
    if not os.path.exists(log_path):
        print("Log not found: " + log_path)
        sys.exit(1)
    try:
        subprocess.run(["tail", "-f", log_path])
    except KeyboardInterrupt:
        pass


def cmd_logs(bot_id, lines=40):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    bot_dir = os.path.join(BOTS_DIR, bot_id)
    for log_name in ("activity.log", "errors.log", "output.log"):
        log_path = os.path.join(bot_dir, log_name)
        if os.path.exists(log_path):
            print("=== " + log_name + " (last " + str(lines) + " lines) ===")
            result = subprocess.run(
                ["tail", "-n", str(lines), log_path],
                capture_output=True,
            )
            if result.stdout:
                print(result.stdout)
        else:
            print("=== " + log_name + ": (empty) ===")


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
        subprocess.run(
            "nohup scriptling " + bot_script + " > " + log_path + " 2>&1 &",
            shell=True,
        )
        started += 1
        print("Started: " + entry)
    print("Done. Started: " + str(started) + "  Skipped (already running): " + str(skipped))


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
            stopped += 1
            print("Stop signal sent to: " + b["id"])
    print("Done. Stop signals sent: " + str(stopped))


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
        killed += 1
        print("Killed: " + b["id"])
    print("Done. Killed: " + str(killed))


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
    subprocess.run(
        "nohup scriptling " + bot_script + " > " + log_path + " 2>&1 &",
        shell=True,
    )
    print("Restarted: " + bot_id)


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
    print("Removed: " + bot_id)


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
        print("Swarm view via " + target["id"] + " (" + gossip_addr + "):")
        print("")
        for n in nodes:
            meta = n.get("metadata", {})
            nid = meta.get("id", n["id"][:16])
            goal = meta.get("goal", "")
            addr = meta.get("gossip_addr", "")
            local = _load_status(nid) if _is_valid_name(nid) else None
            fitness_str = ""
            if local:
                f = local.get("fitness", {})
                fitness_str = "  ticks=" + str(f.get("ticks_alive", 0)) + " spawns=" + str(f.get("spawns", 0)) + " evolutions=" + str(f.get("brain_evolutions", 0))
            print("  " + nid + "  @ " + addr + fitness_str + "  goal: " + goal)
        print("")
        print("Total: " + str(len(nodes)) + " bots")
        c.stop()
    except Exception as e:
        print("Error: " + str(e))
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

        # Copy bot directory then remove runtime-only files
        dest_bot_dir = os.path.join(staging_dir, "bots", bot_id)
        subprocess.run(["cp", "-r", bot_dir, dest_bot_dir])
        for runtime_file in ("memory.db", "output.log", "errors.log", "activity.log"):
            p = os.path.join(dest_bot_dir, runtime_file)
            if os.path.exists(p):
                subprocess.run(["rm", "-rf", p])

        # Copy control script
        subprocess.run(["cp", os.path.join(SCRIPT_DIR, "control.py"), os.path.join(staging_dir, "bin", "control.py")])

        # Copy lib directory (needed for spawn on target machine)
        subprocess.run(["cp", "-r", LIB_DIR, os.path.join(staging_dir, "lib")])

        # Copy .env.example if present
        env_example = os.path.join(PROJECT_DIR, ".env.example")
        if os.path.exists(env_example):
            subprocess.run(["cp", env_example, os.path.join(staging_dir, ".env.example")])

        # Generate bootstrap.sh at project root level
        bootstrap = (
            "#!/bin/bash\nset -e\n"
            "PROJ_DIR=\"$(cd \"$(dirname \"$0\")\" && pwd)\"\n"
            "if [ ! -f \"$PROJ_DIR/.env\" ]; then\n"
            "  echo \"No .env found. Copy .env.example to .env and fill in your credentials.\"\n"
            "  exit 1\n"
            "fi\n"
            "nohup scriptling \"$PROJ_DIR/bots/" + bot_id + "/bot.py\" "
            "> \"$PROJ_DIR/bots/" + bot_id + "/output.log\" 2>&1 &\n"
            "echo \"Started " + bot_id + " (PID $!)\"\n"
            "echo \"Manage with: scriptling bin/control.py list\"\n"
        )
        os.write_file(os.path.join(staging_dir, "bootstrap.sh"), bootstrap)
        subprocess.run(["chmod", "+x", os.path.join(staging_dir, "bootstrap.sh")])

        # Pack it up
        result = subprocess.run(
            ["tar", "czf", archive_path, "-C", PROJECT_DIR, staging],
            capture_output=True,
        )
        if result.returncode != 0:
            print("Export failed: " + str(result.stderr))
            sys.exit(1)
    finally:
        subprocess.run(["rm", "-rf", staging_dir])

    print("Exported: " + archive_path)
    print("Transfer and run:")
    print("  tar xzf " + archive_name + " && cd " + staging + " && cp .env.example .env")
    print("  # edit .env with your credentials")
    print("  bash bootstrap.sh")


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
        print("Restarting stale: " + bot_id)
        cmd_restart(bot_id)
    print("Done. Restarted: " + str(len(stale)))


def cmd_watchdog():
    interval = 30
    print("Watchdog started (checking every " + str(interval) + "s). Press Ctrl+C to stop.")
    while True:
        try:
            bots = _all_bots()
            for b in bots:
                if b.get("status") not in ("running",):
                    continue
                bot_id = b["id"]
                bot_script = os.path.join(BOTS_DIR, bot_id, "bot.py")
                check = subprocess.run(
                    ["pgrep", "-f", bot_script],
                    capture_output=True,
                )
                if check.returncode != 0:
                    print(time.strftime("%Y-%m-%d %H:%M:%S") + "  Crash detected: " + bot_id + "  restarting...")
                    log_path = os.path.join(BOTS_DIR, bot_id, "output.log")
                    subprocess.run(
                        "nohup scriptling " + bot_script + " >> " + log_path + " 2>&1 &",
                        shell=True,
                    )
            time.sleep(interval)
        except KeyboardInterrupt:
            print("Watchdog stopped.")
            break
        except Exception as e:
            print("Watchdog error: " + str(e))
            time.sleep(interval)


def main():
    args = sys.argv
    if len(args) < 2:
        print("Usage: control <command> [options]")
        print("")
        print("Commands:")
        print("  spawn <name> <goal> [k=v ...]  Create a bot")
        print("    Options: base_url=  model=  brain=  seeds=  thinking=false")
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
        print("  logs <bot> [N]        Last N lines of activity/error/output logs")
        print("  tail <bot> [log]      Follow log in real time (default: output.log)")
        print("  send <bot> <msg>      Send a message to a running bot")
        print("  watchdog              Auto-restart crashed bots (runs until Ctrl+C)")
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
            print("Usage: control tail <bot-id> [output|activity|errors]")
            sys.exit(1)
        log_name = args[3] + ".log" if len(args) > 3 else "output.log"
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
