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
DEFAULTS_PATH = os.path.join(PROJECT_DIR, "lib", "defaults.py")
PROMPT_PATH = os.path.join(PROJECT_DIR, "lib", "prompt.py")
ENV_FILE = os.path.join(PROJECT_DIR, ".env")
MODELS_PATH = os.path.join(PROJECT_DIR, "models.json")


def _load_dotenv():
    if not os.path.exists(ENV_FILE):
        return
    content = os.read_file(ENV_FILE)
    for line in content.split("\n"):
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        key = key.strip()
        value = value.strip().strip('"').strip("'")
        if key and key not in os.environ:
            os.environ[key] = value


_load_dotenv()


def _get_default(key, fallback=""):
    return os.environ.get(key, fallback)


def _is_valid_name(name):
    if not name or len(name) > 64:
        return False
    for c in name:
        if not (c.isalnum() or c == "-" or c == "_"):
            return False
    return True


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
        print("Optional key=value pairs (override defaults):")
        print("  api_key=<key>     LLM API key")
        print("  base_url=<url>    LLM API base URL")
        print("  model=<model>     Model name")
        print("  brain=<text>      Initial brain content")
        print("  seeds=<addr,...>  Comma-separated gossip seed addresses to join")
        print("  thinking=false    Disable LLM thinking (for worker bots)")
        print("")
        print("Defaults are loaded from environment variables or .env file:")
        print("  BOT_API_KEY       LLM API key")
        print("  BOT_BASE_URL      LLM API base URL (required if not passed)")
        print("  BOT_MODEL         Model name (required if not passed)")
        sys.exit(0 if len(args) >= 2 and args[1] in ("help", "--help", "-h") else 1)

    name = args[1]
    goal = args[2]

    opts = {
        "api_key": _get_default("BOT_API_KEY"),
        "base_url": _get_default("BOT_BASE_URL"),
        "model": _get_default("BOT_MODEL"),
        "brain": "",
        "seeds": "",
        "thinking": "true",
    }
    for a in args[3:]:
        if "=" in a:
            k, v = a.split("=", 1)
            if k in opts:
                opts[k] = v

    if not opts["base_url"]:
        print("Error: base_url is required. Set BOT_BASE_URL in .env or pass base_url=<url>")
        sys.exit(1)
    if not opts["model"]:
        print("Error: model is required. Set BOT_MODEL in .env or pass model=<model>")
        sys.exit(1)

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
        "thinking": opts["thinking"].lower() != "false",
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
    defaults_src = os.read_file(DEFAULTS_PATH)
    source = _inject_block(source, "# --- DEFAULTS ---", "# --- END DEFAULTS ---", defaults_src)
    if source is None:
        print("Error: bot template is missing DEFAULTS markers.")
        sys.exit(1)
    prompt_src = os.read_file(PROMPT_PATH)
    source = _inject_block(source, "# --- SYSTEM PROMPT ---", "# --- END SYSTEM PROMPT ---", prompt_src)
    if source is None:
        print("Error: bot template is missing SYSTEM PROMPT markers.")
        sys.exit(1)

    models_block = "AVAILABLE_MODELS = []"
    if os.path.exists(MODELS_PATH):
        try:
            models_data = json.loads(os.read_file(MODELS_PATH))
            if models_data:
                models_block = "AVAILABLE_MODELS = " + json.dumps(models_data, indent=4)
        except Exception:
            pass
    source = _inject_block(source, "# --- MODELS ---", "# --- END MODELS ---", models_block)
    if source is None:
        print("Error: bot template is missing MODELS markers.")
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
