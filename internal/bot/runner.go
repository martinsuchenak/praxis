package bot

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	scriptlingllmlib "github.com/martinsuchenak/scriptling-llm-lib"
	"github.com/paularlott/logger"
	scriptling "github.com/paularlott/scriptling"
	"github.com/paularlott/scriptling/extlibs"
	"github.com/paularlott/scriptling/extlibs/agent"
	extai "github.com/paularlott/scriptling/extlibs/ai"
	"github.com/paularlott/scriptling/extlibs/ai/memory"
	netgossip "github.com/paularlott/scriptling/extlibs/net/gossip"
	"github.com/paularlott/scriptling/extlibs/net/multicast"
	"github.com/paularlott/scriptling/stdlib"

	"praxis/internal/config"
)

const (
	backoffBase   = 2 * time.Second
	backoffMax    = 60 * time.Second
	backoffFactor = 2.0
)

// RunnerConfig holds everything a Runner needs to start a bot.
type RunnerConfig struct {
	// WatchdogAddr is the gossip address of the watchdog node (bot connects as peer).
	WatchdogAddr string
	// LogLevel passed to scriptling's logging library.
	LogLevel string
	// ModelsDir is the directory containing .gguf model files. If empty, local
	// model inference is unavailable. The path is added to every bot's allowed
	// filesystem paths so llm.generate() can read GGUF files.
	ModelsDir string
}

// Runner manages the lifecycle of a single embedded scriptling bot.
type Runner struct {
	bot    *Bot
	mgr    *Manager
	pool   *RunnerPool
	base   *scriptling.Scriptling
	cfg    RunnerConfig
	log    logger.Logger
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	crash  int
}

// RunnerPool manages a set of Runners, one per bot.
type RunnerPool struct {
	mu        sync.Mutex
	runners   map[string]*Runner
	base      *scriptling.Scriptling
	cfg       RunnerConfig
	mgr       *Manager
	log       logger.Logger
	factories sync.Map // goroutineID → extlibs.SandboxFactory
}

// NewRunnerPool creates a RunnerPool. Call Start to begin running bots.
func NewRunnerPool(mgr *Manager, cfg RunnerConfig, log logger.Logger) *RunnerPool {
	rp := &RunnerPool{
		runners: make(map[string]*Runner),
		base:    newBaseInterpreter(log),
		cfg:     cfg,
		mgr:     mgr,
		log:     log,
	}

	// Set a single global factory that dispatches per-goroutine.
	dispatch := func() extlibs.SandboxInstance {
		gid := goroutineID()
		if fn, ok := rp.factories.Load(gid); ok {
			return fn.(extlibs.SandboxFactory)()
		}
		child := scriptling.New()
		registerLibraries(child, log)
		return child
	}
	extlibs.SetSandboxFactory(dispatch)
	extlibs.SetBackgroundFactory(dispatch)

	return rp
}

// registerLibraries registers all shared libraries onto p. Reused by both
// newBaseInterpreter and the sandbox/background factory.
func registerLibraries(p *scriptling.Scriptling, log logger.Logger) {
	stdlib.RegisterAll(p)
	extlibs.RegisterRequestsLibrary(p)
	extlibs.RegisterGrepLibrary(p, nil)
	extlibs.RegisterSedLibrary(p, nil)
	extlibs.RegisterGlobLibrary(p, nil)
	extlibs.RegisterRuntimeLibraryAll(p, nil)
	netgossip.Register(p, log)
	multicast.Register(p)
	extai.Register(p)
	memory.Register(p, log)
	_ = agent.Register(p)
	p.RegisterLibrary(scriptlingllmlib.Library)
}

// newBaseInterpreter creates a shared base Scriptling interpreter with all
// libraries registered except per-bot ones (OS, allowed paths). Bot clones
// inherit all registered libraries and can then have OS registered with their
// specific allowed paths.
//
// It also sets the global sandbox and background factories so that
// runtime.sandbox.create() and runtime.background() work in bot scripts.
func newBaseInterpreter(log logger.Logger) *scriptling.Scriptling {
	p := scriptling.New()
	registerLibraries(p, log)
	return p
}

func newChildFactory(allowedPaths []string, log logger.Logger) func() extlibs.SandboxInstance {
	return func() extlibs.SandboxInstance {
		child := scriptling.New()
		registerLibraries(child, log)
		extlibs.RegisterOSLibrary(child, allowedPaths)
		return child
	}
}

// scanGGUFModels returns a sorted list of .gguf file names found in dir.
// Returns nil if the directory is empty or doesn't exist.
func scanGGUFModels(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var models []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > 5 && name[len(name)-5:] == ".gguf" {
			models = append(models, name)
		}
	}
	sort.Strings(models)
	return models
}

// Start starts a bot runner. If the bot is already running, returns an error.
func (rp *RunnerPool) Start(botID string) error {
	rp.mu.Lock()
	defer rp.mu.Unlock()

	if r, ok := rp.runners[botID]; ok {
		if r.isRunning() {
			return fmt.Errorf("bot %q is already running", botID)
		}
	}

	b, err := rp.mgr.Get(botID)
	if err != nil {
		return fmt.Errorf("bot not found: %w", err)
	}

	r := &Runner{
		bot:  b,
		mgr:  rp.mgr,
		pool: rp,
		base: rp.base,
		cfg:  rp.cfg,
		log:  rp.log.WithGroup("runner").With("bot", botID),
		done: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	rp.runners[botID] = r

	_ = rp.mgr.SetStatus(botID, StatusStarting)

	go r.run(ctx)
	return nil
}

// Stop cancels a running bot's context and sets status to stopped.
func (rp *RunnerPool) Stop(botID string) error {
	rp.mu.Lock()
	r, ok := rp.runners[botID]
	rp.mu.Unlock()
	if !ok || !r.isRunning() {
		return fmt.Errorf("bot %q is not running", botID)
	}
	_ = rp.mgr.SetStatus(botID, StatusStopping)
	r.cancel()
	<-r.done
	rp.mgr.RemoveLocks(botID)
	return nil
}

// Kill immediately cancels a running bot's context.
func (rp *RunnerPool) Kill(botID string) error {
	rp.mu.Lock()
	r, ok := rp.runners[botID]
	rp.mu.Unlock()
	if !ok || !r.isRunning() {
		return fmt.Errorf("bot %q is not running", botID)
	}
	_ = rp.mgr.SetStatus(botID, StatusKilled)
	r.cancel()
	<-r.done
	rp.mgr.RemoveLocks(botID)
	return nil
}

// Wait blocks until the bot's runner goroutine exits.
func (rp *RunnerPool) Wait(botID string) {
	rp.mu.Lock()
	r, ok := rp.runners[botID]
	rp.mu.Unlock()
	if ok {
		<-r.done
	}
}

// StopAll stops all running bots.
func (rp *RunnerPool) StopAll() {
	rp.mu.Lock()
	ids := make([]string, 0, len(rp.runners))
	for id := range rp.runners {
		ids = append(ids, id)
	}
	rp.mu.Unlock()

	for _, id := range ids {
		_ = rp.Stop(id)
	}
}

// KillAll kills all running bots.
func (rp *RunnerPool) KillAll() {
	rp.mu.Lock()
	ids := make([]string, 0, len(rp.runners))
	for id := range rp.runners {
		ids = append(ids, id)
	}
	rp.mu.Unlock()

	for _, id := range ids {
		_ = rp.Kill(id)
	}
}

// IsRunning returns true if a bot is actively being managed.
func (rp *RunnerPool) IsRunning(botID string) bool {
	rp.mu.Lock()
	r, ok := rp.runners[botID]
	rp.mu.Unlock()
	return ok && r.isRunning()
}

func (r *Runner) logToFile(msg string) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().UTC().Format("2006-01-02 15:04:05"), msg)
	f, err := os.OpenFile(r.bot.Dir+"/bot.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}

// run is the goroutine loop for a single bot. It runs until ctx is cancelled,
// restarting after crashes with exponential backoff.
func (r *Runner) run(ctx context.Context) {
	defer close(r.done)
	defer func() {
		r.mu.Lock()
		r.cancel = nil
		r.mu.Unlock()
	}()

	for {
		b := r.base.Clone()

		cfg, loadErr := LoadConfig(r.bot.Dir)
		if loadErr != nil {
			r.log.Error("cannot load config", "err", loadErr)
			r.logToFile("ERROR: cannot load config: " + loadErr.Error())
			return
		}
		r.bot.Config = cfg

		script, readErr := os.ReadFile(r.bot.Dir + "/bot.py")
		if readErr != nil {
			r.log.Error("cannot read bot.py", "err", readErr)
			r.logToFile("ERROR: cannot read bot.py: " + readErr.Error())
			return
		}

		// Register per-bot OS library with restricted paths.
		allowedPaths := cfg.AllowedPaths(r.bot.Dir, r.mgr.BotsDir, r.mgr.LocksDir)
		if r.cfg.ModelsDir != "" {
			allowedPaths = append(allowedPaths, r.cfg.ModelsDir)
		}
		extlibs.RegisterOSLibrary(b, allowedPaths)

		// Pre-set CONFIG with seed_addrs and models catalog.
		configDict := cfg.AsDict()
		if r.cfg.WatchdogAddr != "" {
			configDict["seed_addrs"] = []string{r.cfg.WatchdogAddr}
		}
		if appCfg := config.Get(); appCfg != nil {
			if len(appCfg.Models.Catalog) > 0 {
				configDict["models"] = appCfg.ModelsAsInterface()
			}
			configDict["base_url"] = appCfg.Bot.BaseURL
			configDict["tick_interval"] = appCfg.Bot.TickInterval
			configDict["tick_max_iterations"] = appCfg.Bot.TickMaxIter
			configDict["log_verbose"] = appCfg.Bot.LogVerbose
			configDict["log_result_max"] = appCfg.Bot.LogResultMax
			configDict["stale_threshold"] = appCfg.Bot.StaleThreshold
			configDict["script_timeout"] = appCfg.Bot.ScriptTimeout
			configDict["max_backoff"] = appCfg.Bot.MaxBackoff
			configDict["max_concurrent"] = appCfg.Bot.MaxConcurrent
			configDict["http_allowlist"] = appCfg.Bot.HTTPAllowlist
			configDict["shell_allowlist"] = appCfg.Bot.ShellAllowlist
			configDict["stuck_ticks"] = appCfg.Bot.StuckTicks
		}
		if r.cfg.ModelsDir != "" {
			if localModels := scanGGUFModels(r.cfg.ModelsDir); len(localModels) > 0 {
				configDict["local_models"] = localModels
				configDict["models_dir"] = r.cfg.ModelsDir
			}
		}
		if err := b.SetVar("CONFIG", configDict); err != nil {
			r.log.Error("SetVar CONFIG", "err", err)
			r.logToFile("ERROR: SetVar CONFIG: " + err.Error())
			return
		}

		b.SetSourceFile(r.bot.Dir + "/bot.py")

		currentState, _ := LoadState(r.bot.Dir)
		if currentState.Status == StatusStopping || currentState.Status == StatusStopped || currentState.Status == StatusKilled {
			return
		}
		_ = SaveState(r.bot.Dir, &BotState{Status: StatusRunning})

		// Register per-goroutine factory so sandbox/background calls from
		// this bot's script use this bot's allowed paths.
		gid := goroutineID()
		childFactory := newChildFactory(allowedPaths, r.log)
		r.pool.factories.Store(gid, extlibs.SandboxFactory(childFactory))

		_, evalErr := b.EvalWithContext(ctx, string(script))

		r.pool.factories.Delete(gid)

		if ctx.Err() != nil {
			// Clean stop or kill — don't restart.
			state, _ := LoadState(r.bot.Dir)
			if state.Status != StatusKilled {
				_ = SaveState(r.bot.Dir, &BotState{Status: StatusStopped})
			}
			return
		}

		if evalErr != nil {
			r.log.Error("bot crashed", "err", evalErr)
			r.logToFile("ERROR: " + evalErr.Error())
			r.mu.Lock()
			r.crash++
			crashes := r.crash
			r.mu.Unlock()

			_ = SaveState(r.bot.Dir, &BotState{Status: StatusStopped})

			backoff := nextBackoff(crashes)
			r.log.Info("restarting after backoff", "delay", backoff)
			r.logToFile(fmt.Sprintf("restarting in %s (crash #%d)", backoff, crashes))
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
		} else {
			// Clean exit (script returned normally) — don't restart.
			_ = SaveState(r.bot.Dir, &BotState{Status: StatusStopped})
			return
		}
	}
}

func (r *Runner) isRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil
}

// nextBackoff computes exponential backoff capped at backoffMax.
func nextBackoff(crashes int) time.Duration {
	d := float64(backoffBase) * math.Pow(backoffFactor, float64(crashes-1))
	if d > float64(backoffMax) {
		d = float64(backoffMax)
	}
	return time.Duration(d)
}

func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// buf looks like "goroutine 123 [...]"
	s := bytes.TrimSpace(buf[10:n])
	idx := bytes.IndexByte(s, ' ')
	if idx > 0 {
		s = s[:idx]
	}
	id, _ := strconv.ParseUint(string(s), 10, 64)
	return id
}
