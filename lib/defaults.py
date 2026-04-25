import os

MAX_BRAIN_SIZE = 50000
MAX_SPAWN_COUNT = 10
MAX_BRAIN_HISTORY = 5
MULTICAST_ADDR = "239.255.13.37"
MULTICAST_PORT = 19373
MULTICAST_ANNOUNCE_EVERY = 10
BOT_LOG_MAX = int(os.environ.get("BOT_LOG_MAX", str(500 * 1024)))

AGENT_MAX_TOKENS = 50000
AGENT_COMPACTION_THRESHOLD = 70
AGENT_REQUEST_TIMEOUT_MS = 300000

DEFAULT_API_KEY = os.environ.get("BOT_API_KEY", "")
DEFAULT_BASE_URL = os.environ.get("BOT_BASE_URL", "")
DEFAULT_MODEL = os.environ.get("BOT_MODEL", "")
LOG_VERBOSE = os.environ.get("BOT_LOG_VERBOSE", "false").lower() == "true"
LOG_RESULT_MAX = int(os.environ.get("BOT_LOG_RESULT_MAX", "80"))
TICK_INTERVAL = int(os.environ.get("BOT_TICK_INTERVAL", "30"))
GOSSIP_SECRET = os.environ.get("BOT_GOSSIP_SECRET", "")
STALE_THRESHOLD_SEC = int(os.environ.get("BOT_STALE_THRESHOLD", "120"))
SCRIPT_TIMEOUT = int(os.environ.get("BOT_SCRIPT_TIMEOUT", "30"))
MAX_BACKOFF_SEC = int(os.environ.get("BOT_MAX_BACKOFF", "600"))
BOT_MAX_CONCURRENT = int(os.environ.get("BOT_MAX_CONCURRENT", "1"))
TICK_MAX_ITERATIONS = int(os.environ.get("BOT_TICK_MAX_ITERATIONS", "5"))
HTTP_ALLOWLIST = [h.strip() for h in os.environ.get("BOT_HTTP_ALLOWLIST", "").split(",") if h.strip()]
SHELL_ALLOWLIST = [h.strip() for h in os.environ.get("BOT_SHELL_ALLOWLIST", "").split(",") if h.strip()]
