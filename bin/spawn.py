#!/usr/bin/env scriptling

import os
import os.path
import json
import sys
import time

SCRIPT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_DIR = os.path.dirname(SCRIPT_DIR)
BOTS_DIR = os.path.join(PROJECT_DIR, "bots")
TEMPLATE_PATH = os.path.join(PROJECT_DIR, "lib", "botcore.py")


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


def _atomic_write(path, data):
    tmp = path + ".tmp"
    os.write_file(tmp, json.dumps(data, indent=2))
    os.rename(tmp, path)


def main():
    args = sys.argv

    if len(args) < 3 or args[1] in ("help", "--help", "-h"):
        print("Usage: spawn <name> <goal> [key=value ...]")
        print("")
        print("Required:")
        print("  name              Bot name (letters, digits, dash, underscore)")
        print("  goal              Bot goal/objective (quote if multi-word)")
        print("")
        print("Optional key=value pairs:")
        print("  api_key=<key>     LLM API key")
        print("  base_url=<url>    LLM API base URL")
        print("  model=<model>     Model name")
        print("  brain=<text>      Initial brain content")
        print("  seeds=<addr,...>  Comma-separated gossip seed addresses to join")
        sys.exit(0 if len(args) >= 2 and args[1] in ("help", "--help", "-h") else 1)

    name = args[1]
    goal = args[2]

    opts = {
        "api_key": "",
        "base_url": "https://llmrouter.adinko.me/v1",
        "model": "qwen3.6-35b-a3b",
        "brain": "",
        "seeds": "",
    }
    for a in args[3:]:
        if "=" in a:
            k, v = a.split("=", 1)
            if k in opts:
                opts[k] = v

    if not _is_valid_name(name):
        print("Invalid name. Use only letters, digits, dash, underscore (max 64 chars).")
        sys.exit(1)

    seed_addrs = [s.strip() for s in opts["seeds"].split(",") if s.strip()] if opts["seeds"] else []

    if not opts["brain"]:
        opts["brain"] = "I am " + name + ". I was just created. I need to explore and understand my purpose.\n"

    config = {
        "name": name,
        "goal": goal,
        "api_key": opts["api_key"],
        "base_url": opts["base_url"],
        "model": opts["model"],
        "brain": opts["brain"],
        "seed_addrs": seed_addrs,
    }

    if not os.path.exists(BOTS_DIR):
        os.makedirs(BOTS_DIR)

    bot_dir = os.path.join(BOTS_DIR, name)
    if os.path.exists(bot_dir):
        print("Bot already exists: " + name)
        sys.exit(1)

    template = os.read_file(TEMPLATE_PATH)
    source = _inject_config(template, config)
    if source is None:
        print("Error: bot template is missing CONFIG markers.")
        sys.exit(1)

    os.makedirs(bot_dir)
    os.write_file(os.path.join(bot_dir, "bot.py"), source)

    # Write a minimal status so control.py list shows it before first run
    _atomic_write(os.path.join(bot_dir, "status.json"), {
        "id": name,
        "goal": goal,
        "status": "created",
        "created_at": int(time.time()),
        "gossip_addr": "",
        "fitness": {},
    })

    print("Created: " + name)
    print("Dir:     " + bot_dir)
    print("Start:   scriptling bin/control.py start " + name)


main()
