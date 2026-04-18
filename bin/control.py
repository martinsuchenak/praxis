#!/usr/bin/env scriptling

import os
import os.path
import json
import sys
import subprocess
import time

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_DIR = os.path.dirname(SCRIPT_DIR)
BOTS_DIR = os.path.join(PROJECT_DIR, "bots")
SHARED_DIR = os.path.join(PROJECT_DIR, "shared")
REGISTRY_PATH = os.path.join(SHARED_DIR, "registry.json")
BUS_PATH = os.path.join(SHARED_DIR, "message_bus.json")


def _is_valid_name(name):
    if not name or len(name) > 64:
        return False
    for c in name:
        if not (c.isalnum() or c == "-" or c == "_"):
            return False
    return True


def load_registry():
    if not os.path.exists(REGISTRY_PATH):
        return {}
    try:
        return json.loads(os.read_file(REGISTRY_PATH))
    except Exception:
        return {}


def save_registry(data):
    tmp = REGISTRY_PATH + ".tmp"
    os.write_file(tmp, json.dumps(data, indent=2))
    os.rename(tmp, REGISTRY_PATH)


def cmd_list():
    registry = load_registry()
    if not registry:
        print("No bots registered.")
        return
    for bot_id in registry:
        bot = registry[bot_id]
        parent = bot.get("parent", "")
        suffix = " (parent: " + parent + ")" if parent else ""
        print(
            bot_id
            + " | "
            + bot.get("status", "?")
            + " | "
            + bot.get("goal", "?")
            + suffix
        )


def cmd_start(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    registry = load_registry()
    if bot_id not in registry:
        print("Bot not found: " + bot_id)
        sys.exit(1)
    bot_dir = registry[bot_id].get("dir", os.path.join(BOTS_DIR, bot_id))
    bot_script = os.path.join(bot_dir, "bot.py")
    if not os.path.exists(bot_script):
        print("Bot script not found: " + bot_script)
        sys.exit(1)
    log_path = os.path.join(bot_dir, "output.log")
    subprocess.run(
        "nohup scriptling "
        + bot_script
        + " > "
        + log_path
        + " 2>&1 &",
        shell=True,
    )
    registry[bot_id]["status"] = "running"
    registry[bot_id]["started_at"] = int(time.time())
    save_registry(registry)
    print("Started: " + bot_id)


def cmd_stop(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    registry = load_registry()
    if bot_id not in registry:
        print("Bot not found: " + bot_id)
        sys.exit(1)
    registry[bot_id]["status"] = "stopping"
    save_registry(registry)
    subprocess.run(["pkill", "-f", bot_id + "/bot.py"])
    print("Stop signal sent to: " + bot_id)


def cmd_kill(bot_id):
    if not _is_valid_name(bot_id):
        print("Invalid bot name.")
        sys.exit(1)
    registry = load_registry()
    if bot_id not in registry:
        print("Bot not found: " + bot_id)
        sys.exit(1)
    registry[bot_id]["status"] = "stopping"
    save_registry(registry)
    subprocess.run(["pkill", "-f", bot_id + "/bot.py"])
    del registry[bot_id]
    save_registry(registry)
    print("Killed: " + bot_id)


def cmd_messages(bot_id):
    if not os.path.exists(BUS_PATH):
        print("No messages.")
        return
    try:
        messages = json.loads(os.read_file(BUS_PATH))
    except Exception:
        print("No messages.")
        return
    found = False
    for m in messages:
        if m["to"] == bot_id or m["from"] == bot_id:
            found = True
            if m["from"] == bot_id:
                print("-> " + m["to"] + ": " + m["content"])
            else:
                print("<- " + m["from"] + ": " + m["content"])
    if not found:
        print("No messages for " + bot_id)


def main():
    args = sys.argv
    if len(args) < 2:
        print("Usage: control <list|start|stop|kill|messages> [bot-id]")
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
    elif command == "messages":
        if len(args) < 3:
            print("Usage: control messages <bot-id>")
            sys.exit(1)
        cmd_messages(args[2])
    else:
        print("Unknown command: " + command)
        print("Commands: list, start, stop, kill, messages")


main()
