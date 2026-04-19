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


def cmd_list():
    bots = _all_bots()
    if not bots:
        print("No bots found.")
        return
    for b in sorted(bots, key=lambda x: x.get("id", "")):
        fitness = b.get("fitness", {})
        ticks = fitness.get("ticks_alive", 0)
        spawns = fitness.get("spawns", 0)
        gossip = b.get("gossip_addr", "")
        addr_str = "  @ " + gossip if gossip else ""
        print(
            b.get("id", "?")
            + " | " + b.get("status", "?")
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
        c.send_to(target["id"], gossip.MSG_USER, {
            "type": "message",
            "from": "operator",
            "content": message,
        })
        c.stop()
        print("Sent to " + bot_id + ": " + message)
    except Exception as e:
        print("Error: " + str(e))
        sys.exit(1)


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


def main():
    args = sys.argv
    if len(args) < 2:
        print("Usage: control <list|start|stop|kill|logs|send> [bot-id] [options]")
        sys.exit(1)

    command = args[1]
    if command == "list":
        cmd_list()
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
    elif command == "logs":
        if len(args) < 3:
            print("Usage: control logs <bot-id> [lines]")
            sys.exit(1)
        lines = int(args[3]) if len(args) > 3 else 40
        cmd_logs(args[2], lines)
    elif command == "send":
        if len(args) < 4:
            print("Usage: control send <bot-id> <message>")
            sys.exit(1)
        cmd_send(args[2], args[3])
    else:
        print("Unknown command: " + command)
        print("Commands: list, start, stop, kill, logs, send")
        sys.exit(1)


main()
