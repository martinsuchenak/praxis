package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	gotui "github.com/paularlott/cli/tui"
	"github.com/paularlott/logger"

	"praxis/internal/bot"
	"praxis/internal/cluster"
	"praxis/internal/sandbox"
)

type Dashboard struct {
	ui          *gotui.TUI
	botPanel    *gotui.Panel
	detailPanel *gotui.Panel

	mgr  *bot.Manager
	pool *bot.RunnerPool
	node *cluster.Node
	sb   sandbox.Sandbox
	log  logger.Logger

	mu          sync.Mutex
	selectedBot string
	logCancel   context.CancelFunc
	logOffset   int64
	vizActive   bool

	quitMu     sync.Mutex
	quitPending bool
	quitCancel  context.CancelFunc
}

func New(mgr *bot.Manager, pool *bot.RunnerPool, node *cluster.Node, sb sandbox.Sandbox, log logger.Logger) *Dashboard {
	d := &Dashboard{
		mgr:  mgr,
		pool: pool,
		node: node,
		sb:   sb,
		log:  log,
	}

	themeNames := gotui.ThemeNames()
	themeArgs := make([]string, len(themeNames))
	copy(themeArgs, themeNames)

	d.ui = gotui.New(gotui.Config{
		Theme:          gotui.ThemeDefault,
		StatusLeft:     "praxis",
		StatusRight:    "Ctrl+C to exit",
		UserLabel:      "operator",
		AssistantLabel: "bot",
		OnSubmit:       d.onSubmit,
		OnFocusChange:  func(_ *gotui.Panel) {},
		OnInterrupt:    d.handleInterrupt,
		Commands: []*gotui.Command{
			{Name: "select", Description: "Switch log view to a bot", Handler: func(args string) { d.cmdSelect(strings.TrimSpace(args)) }},
			{Name: "list", Description: "List all bots with details", Handler: func(_ string) { d.cmdList() }},
			{Name: "info", Description: "Show full bot config/status [bot]", Handler: func(args string) { d.cmdInfo(strings.TrimSpace(args)) }},
			{Name: "logs", Description: "Show recent log lines [bot] [lines]", Handler: func(args string) { d.cmdLogs(strings.TrimSpace(args)) }},
			{Name: "top", Description: "Scroll log panel to top", Handler: func(_ string) { d.ui.Panel("main").ScrollToTop() }},
			{Name: "start", Description: "Start a bot [bot]", Handler: func(args string) { d.cmdStart(strings.TrimSpace(args)) }},
			{Name: "start-all", Description: "Start all stopped bots", Handler: func(_ string) { d.cmdStartAll() }},
			{Name: "stop", Description: "Stop a bot gracefully [bot]", Handler: func(args string) { d.cmdStop(strings.TrimSpace(args)) }},
			{Name: "stop-all", Description: "Stop all running bots", Handler: func(_ string) { d.cmdStopAll() }},
			{Name: "kill", Description: "Kill a bot immediately [bot]", Handler: func(args string) { d.cmdKill(strings.TrimSpace(args)) }},
			{Name: "kill-all", Description: "Kill all running bots", Handler: func(_ string) { d.cmdKillAll() }},
			{Name: "restart", Description: "Kill and restart a bot [bot]", Handler: func(args string) { d.cmdRestart(strings.TrimSpace(args)) }},
			{Name: "restart-stale", Description: "Restart all stale bots", Handler: func(_ string) { d.cmdRestartStale() }},
			{Name: "remove", Description: "Kill and permanently delete a bot", Handler: func(args string) { d.cmdRemove(strings.TrimSpace(args)) }},
			{Name: "send", Description: "Send a message to a bot <bot> <msg>", Handler: func(args string) { d.cmdSend(strings.TrimSpace(args)) }},
			{Name: "spawn", Description: `Spawn a new bot <name> "<goal>" [model=<m>] [workspace=<w>]`, Handler: func(args string) { d.cmdSpawn(strings.TrimSpace(args)) }},
			{Name: "export", Description: "Export a bot archive <bot> [path]", Handler: func(args string) { d.cmdExport(strings.TrimSpace(args)) }},
			{Name: "workspace", Description: "Manage workspaces: list|add|remove", Handler: func(args string) { d.cmdWorkspace(strings.TrimSpace(args)) }},
			{Name: "theme", Description: "Switch colour theme", Args: themeArgs, Handler: func(args string) { d.cmdTheme(strings.TrimSpace(args)) }},
			{Name: "visualise", Description: "Matrix-style swarm visualisation", Handler: func(_ string) { d.cmdVisualise() }},
			{Name: "exit", Description: "Exit the TUI", Handler: func(_ string) { d.ui.Exit() }},
		},
	})

	accent := gotui.ThemeDefault.Primary
	d.botPanel = d.ui.CreatePanel(gotui.PanelConfig{
		Name:       "bots",
		Width:      -35,
		MinWidth:   30,
		Scrollable: true,
		Title:      "BOTS",
		Color:      &accent,
	})
	d.ui.AddLeft(d.botPanel)

	secondary := gotui.ThemeDefault.Secondary
	d.detailPanel = d.ui.CreatePanel(gotui.PanelConfig{
		Name:       "detail",
		Width:      32,
		MinWidth:   24,
		Scrollable: true,
		Title:      "DETAIL",
		Color:      &secondary,
	})
	d.ui.AddRight(d.detailPanel)

	return d
}

func (d *Dashboard) Run(ctx context.Context) error {
	go d.runRefresh(ctx)

	go func() {
		time.Sleep(200 * time.Millisecond)
		bots, err := d.mgr.List()
		if err == nil && len(bots) > 0 {
			d.selectBot(ctx, bots[0].Config.Name)
		}
	}()

	return d.ui.Run(ctx)
}

func (d *Dashboard) runRefresh(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.refreshBotPanel()
			d.refreshDetailPanel()
		}
	}
}

func (d *Dashboard) refreshBotPanel() {
	bots, err := d.mgr.List()
	if err != nil {
		return
	}

	d.mu.Lock()
	sel := d.selectedBot
	d.mu.Unlock()

	alive := 0
	threshold := staleThreshold()
	for _, b := range bots {
		if b.State.Status == bot.StatusRunning {
			alive++
		}
	}

	theme := d.ui.Theme()
	var sb strings.Builder

	for _, b := range bots {
		cfg := b.Config
		st := b.State

		var marker string
		var color gotui.Color
		switch {
		case b.IsStale(threshold):
			marker = "!"
			color = theme.Error
		case st.Status == bot.StatusRunning:
			marker = "●"
			color = theme.Primary
		case st.Status == bot.StatusStarting:
			marker = "◌"
			color = theme.Secondary
		case st.Status == bot.StatusKilled:
			marker = "✕"
			color = theme.Error
		default:
			marker = "○"
			color = theme.Dim
		}

		name := cfg.Name
		if name == sel {
			name = d.botPanel.Styled(theme.Primary, name)
		}

		header := marker + " " + name + " — " + d.botPanel.Styled(color, st.Status)
		if ticks := st.TicksAlive(); ticks > 0 {
			header += fmt.Sprintf(", %dt", ticks)
		}
		if st.IsLeader {
			header += " ★"
		}
		sb.WriteString(header + "\n")

		goal := cfg.Goal
		if len(goal) > 60 {
			goal = goal[:57] + "..."
		}
		fmt.Fprintf(&sb, "  Goal:     %s\n", goal)
		fmt.Fprintf(&sb, "  Model:    %s\n", cfg.Model)
		fmt.Fprintf(&sb, "  Thinking: %t\n", cfg.Thinking)

		scope := cfg.Scope
		if scope == "" {
			scope = "open"
		}
		fmt.Fprintf(&sb, "  Scope:    %s\n", scope)

		if cfg.Parent != "" {
			fmt.Fprintf(&sb, "  Parent:   %s\n", cfg.Parent)
		}
		if st.GossipAddr != "" {
			fmt.Fprintf(&sb, "  Gossip:   %s\n", st.GossipAddr)
		}

		sb.WriteString("\n")
	}

	if gc := d.node.Cluster(); gc != nil {
		fmt.Fprintf(&sb, "peers: %d\n", len(gc.AliveNodes()))
	}

	d.botPanel.SetTitle(fmt.Sprintf("BOTS %d/%d", alive, len(bots)))
	d.botPanel.SetContent(sb.String())
}

func (d *Dashboard) refreshDetailPanel() {
	theme := d.ui.Theme()
	var sb strings.Builder

	bots, _ := d.mgr.List()

	// Model usage + queue stats (from models.json + actual bots)
	modelBots := make(map[string][]string)
	for _, b := range bots {
		m := b.Config.Model
		if m == "" {
			m = "(none)"
		}
		modelBots[m] = append(modelBots[m], b.Config.Name)
	}

	type modelInfo struct {
		Label       string
		Concurrency int
		Bots        []string
	}
	models := make(map[string]*modelInfo)

	// Seed from models.json
	projectDir := filepath.Dir(d.mgr.BotsDir)
	if data, err := os.ReadFile(filepath.Join(projectDir, "models.json")); err == nil {
		var list []interface{}
		if json.Unmarshal(data, &list) == nil {
			for _, entry := range list {
				m, ok := entry.(map[string]interface{})
				if !ok {
					continue
				}
				id, _ := m["id"].(string)
				if id == "" {
					continue
				}
				mi := &modelInfo{}
				if label, ok := m["label"].(string); ok {
					mi.Label = label
				}
				if conc, ok := m["concurrency"].(float64); ok && conc > 0 {
					mi.Concurrency = int(conc)
				}
				models[id] = mi
			}
		}
	}

	// Merge actual bot usage
	for m, names := range modelBots {
		if _, ok := models[m]; !ok {
			models[m] = &modelInfo{}
		}
		models[m].Bots = names
	}

	// Sort model IDs for stable display
	var modelIDs []string
	for id := range models {
		modelIDs = append(modelIDs, id)
	}
	sort.Strings(modelIDs)

	if len(modelIDs) > 0 {
		sb.WriteString(d.detailPanel.Styled(theme.Primary, "━━━ models ━━━") + "\n\n")
		for _, id := range modelIDs {
			mi := models[id]
			display := id
			if mi.Label != "" {
				display = mi.Label
			}
			fmt.Fprintf(&sb, " %s\n", d.detailPanel.Styled(theme.Text, display))

			running := 0
			for _, n := range mi.Bots {
				if d.pool.IsRunning(n) {
					running++
				}
			}

			var stats string
			if mi.Concurrency > 0 {
				stats = fmt.Sprintf("bots:%d run:%d max:%d", len(mi.Bots), running, mi.Concurrency)
			} else {
				stats = fmt.Sprintf("bots:%d run:%d", len(mi.Bots), running)
			}
			sanitized := sanitizeModel(id)
			queueDir := filepath.Join(d.mgr.LocksDir, sanitized)
			if queueCount := countQueueTickets(queueDir); queueCount > 0 {
				stats += " " + d.detailPanel.Styled(theme.Error, fmt.Sprintf("q:%d", queueCount))
			}
			fmt.Fprintf(&sb, "   %s\n", stats)
		}
	}

	// Workspace overview
	type wsInfo struct {
		Path  string
		Scope string
		Bots  []string
	}
	wsMap := make(map[string]*wsInfo)
	for _, b := range bots {
		w := b.Config.Workspace
		if w == "" {
			w = "(none)"
		}
		if _, ok := wsMap[w]; !ok {
			wsMap[w] = &wsInfo{}
		}
		wsMap[w].Bots = append(wsMap[w].Bots, b.Config.Name)
	}

	// Load workspace details from workspaces.json
	projectDir = filepath.Dir(d.mgr.BotsDir)
	if data, readErr := os.ReadFile(filepath.Join(projectDir, "workspaces.json")); readErr == nil {
		var wsConfig map[string]interface{}
		if jsonErr := json.Unmarshal(data, &wsConfig); jsonErr == nil {
			for name, entry := range wsConfig {
				if _, ok := wsMap[name]; !ok {
					wsMap[name] = &wsInfo{}
				}
				switch v := entry.(type) {
				case string:
					wsMap[name].Path = v
				case map[string]interface{}:
					if p, ok := v["path"].(string); ok {
						wsMap[name].Path = p
					}
					if ds, ok := v["default_scope"].(string); ok {
						wsMap[name].Scope = ds
					}
				}
			}
		}
	}

	if len(wsMap) > 0 {
		sb.WriteString("\n" + d.detailPanel.Styled(theme.Primary, "━━━ workspaces ━━━") + "\n\n")
		for name, info := range wsMap {
			fmt.Fprintf(&sb, " %s\n", d.detailPanel.Styled(theme.Text, name))
			parts := []string{}
			if len(info.Bots) > 0 {
				parts = append(parts, fmt.Sprintf("bots:%d", len(info.Bots)))
			}
			if info.Scope != "" {
				parts = append(parts, info.Scope)
			}
			if len(parts) > 0 {
				fmt.Fprintf(&sb, "   %s\n", strings.Join(parts, " "))
			}
		}
	}

	d.detailPanel.SetContent(sb.String())
}

func (d *Dashboard) selectBot(ctx context.Context, name string) {
	d.mu.Lock()
	if d.logCancel != nil {
		d.logCancel()
	}
	d.selectedBot = name
	d.logOffset = 0
	d.vizActive = false
	logCtx, cancel := context.WithCancel(ctx)
	d.logCancel = cancel
	d.mu.Unlock()

	main := d.ui.Panel("main")
	main.Clear()
	main.SetTitle(name + " — bot.log")
	go d.tailLog(logCtx, name, main)
}

func (d *Dashboard) tailLog(ctx context.Context, name string, p *gotui.Panel) {
	logPath := d.mgr.BotDir(name) + "/bot.log"
	var offset int64
	buf := make([]byte, 32*1024)

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f, err := os.Open(logPath)
			if err != nil {
				continue
			}
			var writeBuf strings.Builder
			for {
				n, readErr := f.ReadAt(buf, offset)
				if n > 0 {
					writeBuf.Write(buf[:n])
					offset += int64(n)
				}
				if readErr != nil || n == 0 {
					break
				}
			}
			_ = f.Close()
			if writeBuf.Len() > 0 {
				s := strings.TrimSuffix(writeBuf.String(), "\n")
				p.WriteString(s + "\n")
			}
		}
	}
}

func (d *Dashboard) handleInterrupt() {
	d.quitMu.Lock()
	defer d.quitMu.Unlock()

	if d.quitPending {
		d.quitPending = false
		if d.quitCancel != nil {
			d.quitCancel()
		}
		d.ui.Exit()
		return
	}

	d.quitPending = true

	bots, _ := d.mgr.List()
	running := 0
	for _, b := range bots {
		if b.State.Status == bot.StatusRunning || b.State.Status == bot.StatusStarting {
			running++
		}
	}

	msg := "Press Ctrl+C again within 3s to quit."
	if running > 0 {
		msg = fmt.Sprintf("%d bot(s) still running. Press Ctrl+C again within 3s to quit, or keep typing to cancel.", running)
	}
	d.ui.AddMessage(gotui.RoleSystem, msg)

	ctx, cancel := context.WithCancel(context.Background())
	d.quitCancel = cancel

	go func() {
		select {
		case <-time.After(3 * time.Second):
			d.quitMu.Lock()
			if d.quitPending {
				d.quitPending = false
				d.ui.AddMessage(gotui.RoleSystem, "Quit cancelled.")
			}
			d.quitMu.Unlock()
		case <-ctx.Done():
		}
	}()
}

func (d *Dashboard) clearQuitPending() {
	d.quitMu.Lock()
	defer d.quitMu.Unlock()
	if d.quitPending {
		d.quitPending = false
		if d.quitCancel != nil {
			d.quitCancel()
		}
	}
}

func (d *Dashboard) onSubmit(text string) {
	d.clearQuitPending()

	d.mu.Lock()
	sel := d.selectedBot
	d.mu.Unlock()

	if sel == "" {
		d.ui.Panel("main").WriteString("[no bot selected — use /select <name>]\n")
		return
	}

	d.ui.AddMessage(gotui.RoleUser, text)

	if err := d.node.SendMessage(sel, text); err != nil {
		main := d.ui.Panel("main")
		main.WriteString(main.Styled(d.ui.Theme().Error, "[send failed: "+err.Error()+"]") + "\n")
		return
	}
	d.ui.AddMessageAs(gotui.RoleAssistant, sel, "message sent")
}

func (d *Dashboard) cmdSelect(name string) {
	if name == "" {
		d.showInfo("usage: /select <bot>")
		return
	}
	if _, err := d.mgr.Get(name); err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}
	d.selectBot(d.ui.Context(), name)
}

func (d *Dashboard) cmdList() {
	bots, err := d.mgr.List()
	if err != nil {
		d.showInfo(fmt.Sprintf("list error: %v", err))
		return
	}
	if len(bots) == 0 {
		d.showInfo("no bots found")
		return
	}

	threshold := staleThreshold()
	sort.Slice(bots, func(i, j int) bool {
		return bots[i].Config.Name < bots[j].Config.Name
	})

	nameW := 4
	for _, b := range bots {
		if len(b.Config.Name) > nameW {
			nameW = len(b.Config.Name)
		}
	}

	main := d.ui.Panel("main")
	theme := d.ui.Theme()
	var sb strings.Builder
	for _, b := range bots {
		statusStr := b.State.Status
		if b.IsStale(threshold) {
			statusStr = "STALE"
		}
		line := fmt.Sprintf("%-*s  ", nameW, b.Config.Name)
		sb.WriteString(main.Styled(theme.Text, line))
		switch {
		case b.IsStale(threshold):
			sb.WriteString(main.Styled(theme.Error, statusStr))
		case b.State.Status == bot.StatusRunning:
			sb.WriteString(main.Styled(theme.Primary, statusStr))
		case b.State.Status == bot.StatusKilled:
			sb.WriteString(main.Styled(theme.Error, statusStr))
		default:
			sb.WriteString(main.Styled(theme.Dim, statusStr))
		}
		fmt.Fprintf(&sb, "  ticks=%-6d spawns=%d", b.State.TicksAlive(), b.State.Spawns())
		if b.State.GossipAddr != "" {
			sb.WriteString("  @ " + b.State.GossipAddr)
		}
		if b.State.IsLeader {
			sb.WriteString(" ★")
		}
		sb.WriteString("\n")
		sb.WriteString("  " + b.Config.Goal + "\n")
	}
	main.WriteString(sb.String())
}

func (d *Dashboard) cmdStart(name string) {
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /start <bot>")
		return
	}
	b, err := d.mgr.Get(name)
	if err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}
	switch b.State.Status {
	case bot.StatusRunning, bot.StatusStarting:
		d.showInfo(fmt.Sprintf("%s is already %s", name, b.State.Status))
		return
	}
	if err := d.pool.Start(name); err != nil {
		d.showInfo(fmt.Sprintf("start %s: %v", name, err))
		return
	}
	if !d.vizActive {
		d.selectBot(d.ui.Context(), name)
	}
	d.showInfo(fmt.Sprintf("started %s", name))
}

func (d *Dashboard) cmdStartAll() {
	bots, err := d.mgr.List()
	if err != nil {
		d.showInfo(fmt.Sprintf("error: %v", err))
		return
	}
	started, skipped := 0, 0
	for _, b := range bots {
		switch b.State.Status {
		case bot.StatusRunning, bot.StatusStarting:
			skipped++
			continue
		}
		if err := d.mgr.SetStatus(b.Config.Name, bot.StatusCreated); err != nil {
			continue
		}
		started++
	}
	d.showInfo(fmt.Sprintf("started=%d skipped=%d", started, skipped))
}

func (d *Dashboard) cmdStop(name string) {
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /stop <bot>")
		return
	}
	if err := d.pool.Stop(name); err != nil {
		d.showInfo(fmt.Sprintf("stop %s: %v", name, err))
		return
	}
	d.showInfo(fmt.Sprintf("stopping %s", name))
}

func (d *Dashboard) cmdStopAll() {
	bots, err := d.mgr.List()
	if err != nil {
		d.showInfo(fmt.Sprintf("error: %v", err))
		return
	}
	var names []string
	for _, b := range bots {
		if b.State.Status == bot.StatusRunning || b.State.Status == bot.StatusStarting {
			names = append(names, b.Config.Name)
		}
	}
	for _, name := range names {
		go func(n string) { _ = d.pool.Stop(n) }(name)
	}
	d.showInfo(fmt.Sprintf("stopping %d bots", len(names)))
}

func (d *Dashboard) cmdKill(name string) {
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /kill <bot>")
		return
	}
	if err := d.pool.Kill(name); err != nil {
		d.showInfo(fmt.Sprintf("kill %s: %v", name, err))
		return
	}
	d.showInfo(fmt.Sprintf("killed %s", name))
}

func (d *Dashboard) cmdKillAll() {
	bots, err := d.mgr.List()
	if err != nil {
		d.showInfo(fmt.Sprintf("error: %v", err))
		return
	}
	var names []string
	for _, b := range bots {
		if b.State.Status == bot.StatusRunning || b.State.Status == bot.StatusStarting {
			names = append(names, b.Config.Name)
		}
	}
	for _, name := range names {
		go func(n string) { _ = d.pool.Kill(n) }(name)
	}
	d.showInfo(fmt.Sprintf("killing %d bots", len(names)))
}

func (d *Dashboard) cmdRestart(name string) {
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /restart <bot>")
		return
	}
	_ = d.pool.Kill(name)
	time.Sleep(300 * time.Millisecond)
	if err := d.pool.Start(name); err != nil {
		d.showInfo(fmt.Sprintf("restart %s: %v", name, err))
		return
	}
	if !d.vizActive {
		d.selectBot(d.ui.Context(), name)
	}
	d.showInfo(fmt.Sprintf("restarted %s", name))
}

func (d *Dashboard) cmdRestartStale() {
	bots, err := d.mgr.List()
	if err != nil {
		d.showInfo(fmt.Sprintf("error: %v", err))
		return
	}
	threshold := staleThreshold()
	var names []string
	for _, b := range bots {
		if b.IsStale(threshold) {
			names = append(names, b.Config.Name)
		}
	}
	for _, name := range names {
		go func(n string) {
			_ = d.pool.Kill(n)
			time.Sleep(300 * time.Millisecond)
			_ = d.pool.Start(n)
		}(name)
	}
	if len(names) == 0 {
		d.showInfo("no stale bots found")
	} else {
		d.showInfo(fmt.Sprintf("restarting %d stale bots", len(names)))
	}
}

func (d *Dashboard) cmdRemove(name string) {
	if name == "" {
		d.showInfo("usage: /remove <bot>")
		return
	}
	if _, err := d.mgr.Get(name); err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}
	d.mgr.RemoveLocks(name)
	go func() { _ = d.pool.Kill(name) }()
	if err := d.mgr.Delete(name); err != nil {
		return
	}
	d.mu.Lock()
	if d.selectedBot == name {
		d.selectedBot = ""
	}
	d.mu.Unlock()
	d.showInfo(fmt.Sprintf("removed %s", name))
}

func (d *Dashboard) cmdSend(args string) {
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		d.showInfo("usage: /send <bot> <message>")
		return
	}
	target := parts[0]
	message := parts[1]
	if _, err := d.mgr.Get(target); err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", target))
		return
	}
	d.ui.AddMessage(gotui.RoleUser, message)
	if err := d.node.SendMessage(target, message); err != nil {
		main := d.ui.Panel("main")
		main.WriteString(main.Styled(d.ui.Theme().Error, "[send failed: "+err.Error()+"]") + "\n")
		return
	}
	d.ui.AddMessageAs(gotui.RoleAssistant, target, "message sent")
}

func (d *Dashboard) cmdInfo(name string) {
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /info <bot>")
		return
	}
	b, err := d.mgr.Get(name)
	if err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}

	cfg := b.Config
	st := b.State
	scope := cfg.Scope
	if scope == "" {
		scope = "open"
	}

	main := d.ui.Panel("main")
	theme := d.ui.Theme()

	var sb strings.Builder
	sb.WriteString(main.Styled(theme.Primary, "━━━ "+name+" ━━━") + "\n\n")

	fmt.Fprintf(&sb, "  Goal:     %s\n", cfg.Goal)
	fmt.Fprintf(&sb, "  Model:    %s\n", cfg.Model)
	fmt.Fprintf(&sb, "  Thinking: %t\n", cfg.Thinking)

	sb.WriteString("  Status:   ")
	switch st.Status {
	case bot.StatusRunning:
		sb.WriteString(main.Styled(theme.Primary, st.Status) + "\n")
	case bot.StatusKilled:
		sb.WriteString(main.Styled(theme.Error, st.Status) + "\n")
	default:
		sb.WriteString(st.Status + "\n")
	}

	fmt.Fprintf(&sb, "  Scope:    %s\n", scope)

	if cfg.Parent != "" {
		fmt.Fprintf(&sb, "  Parent:   %s\n", cfg.Parent)
	}
	if st.GossipAddr != "" {
		fmt.Fprintf(&sb, "  Gossip:   %s\n", st.GossipAddr)
	}
	if st.IsLeader {
		sb.WriteString("  " + main.Styled(theme.Secondary, "★ leader") + "\n")
	}
	if st.LastTickTS > 0 {
		fmt.Fprintf(&sb, "  LastTick: %s\n", time.Unix(st.LastTickTS, 0).Format("2006-01-02 15:04:05"))
	}
	if ticks := st.TicksAlive(); ticks > 0 {
		fmt.Fprintf(&sb, "  Ticks:    %d\n", ticks)
	}
	if spawns := st.Spawns(); spawns > 0 {
		fmt.Fprintf(&sb, "  Spawns:   %d\n", spawns)
	}
	if cfg.CreatedAt > 0 {
		fmt.Fprintf(&sb, "  Created:  %s\n", time.Unix(cfg.CreatedAt, 0).Format("2006-01-02 15:04:05"))
	}

	fmt.Fprintf(&sb, "\n  Dir:      %s\n", b.Dir)

	sb.WriteString("\n  Allowed paths:\n")
	paths := cfg.AllowedPaths(b.Dir, d.mgr.BotsDir, d.mgr.LocksDir)
	for _, p := range paths {
		fmt.Fprintf(&sb, "    %s\n", p)
	}

	if cfg.Workspace != "" {
		fmt.Fprintf(&sb, "\n  Workspace: %s\n", cfg.Workspace)
		if cfg.WorkspacePath != "" {
			fmt.Fprintf(&sb, "  WS Path:   %s\n", cfg.WorkspacePath)
		}
	}
	if len(cfg.AllowedWorkspaces) > 0 {
		fmt.Fprintf(&sb, "  Allowed WS: %s\n", strings.Join(cfg.AllowedWorkspaces, ", "))
	}
	if cfg.GossipSecret != "" {
		fmt.Fprintf(&sb, "  Secret:    %s\n", maskSecret(cfg.GossipSecret))
	}

	if d.pool.IsRunning(name) {
		sb.WriteString("\n  " + main.Styled(theme.Primary, "Runner: active (in-process)") + "\n")
	} else {
		sb.WriteString("\n  " + main.Styled(theme.Dim, "Runner: idle") + "\n")
	}

	main.WriteString(sb.String())
}

func (d *Dashboard) cmdLogs(args string) {
	var name string
	lines := 40
	if args != "" {
		parts := strings.Fields(args)
		if len(parts) >= 1 {
			name = parts[0]
		}
		if len(parts) >= 2 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
				lines = n
			}
		}
	}
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /logs [bot] [lines]")
		return
	}
	if _, err := d.mgr.Get(name); err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}

	botDir := d.mgr.BotDir(name)
	var sb strings.Builder
	for _, logName := range []string{"bot.log", "output.log"} {
		logPath := filepath.Join(botDir, logName)
		fmt.Fprintf(&sb, "--- %s (last %d lines) ---\n", logName, lines)
		data, err := readLastN(logPath, lines)
		if err != nil {
			sb.WriteString("(empty)\n")
		} else {
			sb.WriteString(data)
		}
	}
	d.ui.Panel("main").WriteString(sb.String())
}

func (d *Dashboard) cmdSpawn(args string) {
	name, goal, opts := parseSpawnArgs(args)
	if name == "" || goal == "" {
		d.showInfo(`usage: /spawn <name> "<goal>" [model=<m>] [workspace=<w>] [scope=<s>]`)
		return
	}
	model := opts["model"]
	if model == "" {
		model = os.Getenv("BOT_MODEL")
	}
	cfg := &bot.BotConfig{
		Name:  name,
		Goal:  goal,
		Model: model,
	}
	if ws := opts["workspace"]; ws != "" {
		wsPath, wsSecret, wsScope, err := d.resolveWorkspace(ws)
		if err != nil {
			d.showInfo(fmt.Sprintf("workspace: %v", err))
			return
		}
		cfg.Workspace = ws
		cfg.WorkspacePath = wsPath
		if wsSecret != "" {
			cfg.GossipSecret = wsSecret
		}
		if cfg.Scope == "" && wsScope != "" {
			cfg.Scope = wsScope
		}
	}
	if scope := opts["scope"]; scope != "" {
		cfg.Scope = scope
	}

	d.ui.StartSpinner("spawning " + name)
	defer d.ui.StopSpinner()

	if err := d.mgr.Create(cfg); err != nil {
		d.showInfo(fmt.Sprintf("spawn error: %v", err))
		return
	}
	if err := d.pool.Start(name); err != nil && !d.pool.IsRunning(name) {
		d.showInfo(fmt.Sprintf("spawned %s but start failed: %v", name, err))
		return
	}
	if !d.vizActive {
		d.selectBot(d.ui.Context(), name)
	}
	d.showInfo(fmt.Sprintf("spawned and started %s", name))
}

func (d *Dashboard) cmdExport(args string) {
	parts := strings.Fields(args)
	if len(parts) < 1 || parts[0] == "" {
		d.showInfo("usage: /export <bot> [path]")
		return
	}
	name := parts[0]
	b, err := d.mgr.Get(name)
	if err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}
	outPath := name + ".tar.gz"
	if len(parts) >= 2 && parts[1] != "" {
		outPath = parts[1]
	}

	d.ui.StartSpinner("exporting " + name)
	defer d.ui.StopSpinner()

	if err := bot.Export(b, outPath); err != nil {
		d.showInfo(fmt.Sprintf("export %s: %v", name, err))
		return
	}
	d.showInfo(fmt.Sprintf("exported %s → %s", name, outPath))
}

func (d *Dashboard) cmdTheme(name string) {
	if name == "" {
		d.showInfo("usage: /theme <name>")
		return
	}
	th, ok := gotui.ThemeByName(name)
	if !ok {
		d.showInfo(fmt.Sprintf("unknown theme: %s (available: %s)", name, strings.Join(gotui.ThemeNames(), ", ")))
		return
	}
	d.ui.SetTheme(th)
	d.botPanel.SetColor(th.Primary)
	d.detailPanel.SetColor(th.Secondary)
	d.showInfo(fmt.Sprintf("theme: %s", name))
}

func (d *Dashboard) cmdVisualise() {
	d.mu.Lock()
	if d.logCancel != nil {
		d.logCancel()
		d.logCancel = nil
	}
	d.selectedBot = ""
	d.vizActive = true
	ctx, cancel := context.WithCancel(d.ui.Context())
	d.logCancel = cancel
	d.mu.Unlock()

	main := d.ui.Panel("main")
	main.Clear()
	main.SetTitle("SWARM — /select to exit")

	v := &matrixVis{
		d:       d,
		panel:   main,
		drops:   make([]drop, 0),
		pending: make(map[string]bool),
	}
	go v.run(ctx)
}

var matrixGlyphs = []rune(
	"ﾊﾐﾋｰｳｼﾅﾓﾆｻﾜﾂｵﾘｱﾎﾃﾏｹﾒｴｶｷﾑﾕﾗｾﾈｽﾀﾇﾍ" +
		"0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"∀∂∃∅∆∇∈∉∋∑∏∛∜⋅√∞∠∧∨∩∪∫∴∝≢≤≥⊂⊃⊆⊇")

type matrixVis struct {
	d          *Dashboard
	panel      *gotui.Panel
	drops      []drop
	frame      int
	pendingMu  sync.Mutex
	pending    map[string]bool
}

type drop struct {
	col   int
	y     float64
	speed float64
	len   int
	chars []rune
}

type botNode struct {
	name    string
	status  string
	ticks   int64
	x, y    int
	parent  string
	leader  bool
	stale   bool
	avatar  []string
	dir     string
	active  bool
}

type maskInfo struct {
	mask   byte
	node   int
	avRow  int
	avCol  int
}

// Green brightness gradient from brightest to darkest
var greenBright = gotui.Color(0x00FF41)
var greenMid = gotui.Color(0x00CC33)
var greenDim = gotui.Color(0x009922)
var greenDark = gotui.Color(0x005511)
var greenDarkest = gotui.Color(0x003308)

var glowEdge = gotui.Color(0x006622)
var glowFill = gotui.Color(0x008833)
var glowFeature = gotui.Color(0x22AA44)
var glowHighlight = gotui.Color(0x44CC66)

func greenFade(depth, total int) gotui.Color {
	ratio := float64(depth) / float64(total)
	switch {
	case ratio < 0.08:
		return greenDim
	case ratio < 0.2:
		return greenDark
	case ratio < 0.4:
		return greenDarkest
	default:
		return greenDarkest
	}
}

func (v *matrixVis) triggerAvatar(name, goal string) {
	v.pendingMu.Lock()
	if v.pending[name] {
		v.pendingMu.Unlock()
		return
	}
	v.pending[name] = true
	v.pendingMu.Unlock()

	v.d.generateAvatar(name, goal)
}

func (v *matrixVis) run(ctx context.Context) {
	ticker := time.NewTicker(67 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.render()
			v.frame++
		}
	}
}

func (v *matrixVis) render() {
	w, h := v.panel.Size()
	if w < 10 || h < 5 {
		return
	}

	theme := v.d.ui.Theme()

	// --- Manage dense rain drops ---
	targetDrops := w * 2
	for len(v.drops) < targetDrops {
		v.drops = append(v.drops, drop{
			col:   rand.Intn(w),
			y:     -rand.Float64() * float64(h*3),
			speed: 0.4 + rand.Float64()*1.2,
			len:   5 + rand.Intn(20),
			chars: nil,
		})
	}
	for len(v.drops) > targetDrops {
		v.drops = v.drops[:len(v.drops)-1]
	}

	// Spawn new chars at head of each drop
	for i := range v.drops {
		d := &v.drops[i]
		d.y += d.speed

		// Randomly mutate existing chars
		if len(d.chars) > 0 && rand.Float64() < 0.1 {
			idx := rand.Intn(len(d.chars))
			d.chars[idx] = randomGlyph()
		}

		// Add new char at head
		d.chars = append(d.chars, randomGlyph())
		if len(d.chars) > d.len {
			d.chars = d.chars[len(d.chars)-d.len:]
		}

		// Respawn if fully off screen
		if d.y-float64(len(d.chars)) > float64(h) {
			d.y = -rand.Float64() * 20
			d.speed = 0.4 + rand.Float64()*1.2
			d.len = 5 + rand.Intn(20)
			d.chars = d.chars[:0]
		}
	}

	// --- Bot nodes ---
	bots, _ := v.d.mgr.List()
	n := len(bots)
	nodes := make([]botNode, 0, n)

	gridCols := int(math.Ceil(math.Sqrt(float64(n) * float64(w) / float64(max(h, 1)))))
	if gridCols < 1 {
		gridCols = 1
	}
	gridRows := int(math.Ceil(float64(n) / float64(gridCols)))
	if gridRows < 1 {
		gridRows = 1
	}

	for i, b := range bots {
		gc := i % gridCols
		gr := i / gridCols
		x := int(float64(w) * (float64(gc) + 0.5) / float64(gridCols))
		y := int(float64(h) * (float64(gr) + 0.5) / float64(gridRows))
		if x < 8 {
			x = 8
		}
		if x >= w-8 {
			x = w - 9
		}
		if y < 6 {
			y = 6
		}
		if y >= h-6 {
			y = h - 7
		}

		active := false
		logPath := filepath.Join(b.Dir, "bot.log")
		if info, err := os.Stat(logPath); err == nil {
			active = time.Since(info.ModTime()) < 3*time.Second
		}

		avatar := loadAvatar(b.Dir)
		if avatar == nil {
			v.triggerAvatar(b.Config.Name, b.Config.Goal)
		}
		nodes = append(nodes, botNode{
			name:   b.Config.Name,
			status: b.State.Status,
			ticks:  b.State.TicksAlive(),
			x:      x,
			y:      y,
			parent: b.Config.Parent,
			leader: b.State.IsLeader,
			stale:  b.IsStale(staleThreshold()),
			avatar: avatar,
			dir:    b.Dir,
			active: active,
		})
	}

	// --- Build output ---
	var sb strings.Builder
	for row := 0; row < h; row++ {
		buf := make([]byte, 0, w*12)
		for col := 0; col < w; col++ {
			// Labels, connections, aura (block rain)
			if ch, color, ok := v.botOverlay(col, row, nodes, theme); ok {
				buf = append(buf, v.panel.Styled(color, string(ch))...)
				continue
			}

			// Avatar mask: rain glyph passes through, mask tints the colour
			if mi, ok := v.avatarMaskAt(col, row, nodes); ok {
				nd := &nodes[mi.node]
				var ch rune
				if rainCh, _, hasRain := v.rainCell(col, row); hasRain {
					ch = rainCh
				} else if nrCh, _, hasNr := v.nodeRainCell(col, row, nodes); hasNr {
					ch = nrCh
				} else {
					ch = avatarGlyph(nd.name, mi.avRow, mi.avCol, v.frame)
				}
				mask := mi.mask
				if nd.active && mask < '4' {
					mask++
				}
				buf = append(buf, v.panel.Styled(maskColor(mask, nd.active), string(ch))...)
				continue
			}

			// Matrix rain character
			if ch, color, ok := v.rainCell(col, row); ok {
				buf = append(buf, v.panel.Styled(color, string(ch))...)
				continue
			}

			// Extra rain near bot nodes
			if ch, color, ok := v.nodeRainCell(col, row, nodes); ok {
				buf = append(buf, v.panel.Styled(color, string(ch))...)
				continue
			}

			// Sparse background chars (ambient noise)
			if rand.Float64() < 0.06 {
				buf = append(buf, v.panel.Styled(greenDarkest, string(randomGlyph()))...)
			} else {
				buf = append(buf, ' ')
			}
		}
		sb.Write(buf)
		if row < h-1 {
			sb.WriteByte('\n')
		}
	}

	v.panel.SetContent(sb.String())
}

func (v *matrixVis) rainCell(col, row int) (rune, gotui.Color, bool) {
	if col >= len(v.drops) {
		return 0, 0, false
	}
	d := &v.drops[col]
	headY := int(d.y)
	tailY := headY - len(d.chars)
	if row > headY || row <= tailY {
		return 0, 0, false
	}
	idx := len(d.chars) - 1 - (headY - row)
	if idx < 0 || idx >= len(d.chars) {
		return 0, 0, false
	}
	return d.chars[idx], greenFade(headY-row, len(d.chars)), true
}

func (v *matrixVis) nodeRainCell(col, row int, nodes []botNode) (rune, gotui.Color, bool) {
	for i := range nodes {
		nd := &nodes[i]
		dx := col - nd.x
		if dx < -10 || dx > 10 {
			continue
		}

		seed := uint32(i*37+dx*13) + uint32(nd.x*7)
		tailLen := 5 + int(seed%5)
		speed := 1 + int(seed%3)
		period := 25 + int(seed%20)

		head := (int(seed) + v.frame*speed/2) % period

		dist := head - row
		for dist < 0 {
			dist += period
		}

		if dist < tailLen {
			glyph := matrixGlyphs[int(seed+uint32(v.frame/4+row*3))%len(matrixGlyphs)]
			return glyph, greenFade(dist, tailLen), true
		}
	}
	return 0, 0, false
}

func (v *matrixVis) avatarMaskAt(col, row int, nodes []botNode) (maskInfo, bool) {
	for i := range nodes {
		nd := &nodes[i]
		if len(nd.avatar) < 5 {
			continue
		}
		avatarH := len(nd.avatar)
		halfW := len(nd.avatar[0]) / 2
		dx := col - nd.x
		dy := row - nd.y
		nameOffset := 2
		if nd.leader {
			nameOffset = 3
		}
		avatarTop := -nameOffset - avatarH
		avatarRow := dy - avatarTop
		if avatarRow >= 0 && avatarRow < avatarH && dx >= -halfW && dx <= halfW {
			line := nd.avatar[avatarRow]
			charIdx := dx + halfW
			if charIdx >= 0 && charIdx < len(line) && line[charIdx] != '0' {
				return maskInfo{line[charIdx], i, avatarRow, charIdx}, true
			}
		}
	}
	return maskInfo{}, false
}

func maskColor(mask byte, active bool) gotui.Color {
	if active {
		switch mask {
		case '1':
			return glowFill
		case '2':
			return glowFeature
		case '3':
			return glowHighlight
		case '4':
			return 0xFFFFFF
		default:
			return glowFill
		}
	}
	switch mask {
	case '1':
		return glowEdge
	case '2':
		return glowFill
	case '3':
		return glowFeature
	case '4':
		return glowHighlight
	default:
		return glowEdge
	}
}

func (v *matrixVis) botOverlay(col, row int, nodes []botNode, theme *gotui.Theme) (rune, gotui.Color, bool) {
	for i := range nodes {
		nd := &nodes[i]
		dx := col - nd.x
		dy := row - nd.y

		// Name row (y-1)
		nameRunes := []rune(nd.name)
		if dy == -1 && dx >= -len(nameRunes)/2 && dx < (len(nameRunes)+1)/2 {
			idx := dx + len(nameRunes)/2
			if idx >= 0 && idx < len(nameRunes) {
				return nameRunes[idx], greenBright, true
			}
		}

		// Node center
		if dx == 0 && dy == 0 {
			pulse := v.frame % 4
			switch {
			case nd.stale:
				return '!', theme.Error, true
			case nd.status == bot.StatusRunning:
				return rune("◈◆◈◇"[pulse]), greenBright, true
			case nd.status == bot.StatusStarting:
				return '◉', greenMid, true
			case nd.status == bot.StatusKilled:
				return '✕', theme.Error, true
			default:
				return '○', greenDark, true
			}
		}

		// Ticks bar (y+1)
		if nd.ticks > 0 && dy == 1 {
			barLen := (int(nd.ticks) % 6) + 3
			startX := -barLen / 2
			if dx >= startX && dx < startX+barLen {
				bars := "▁▂▃▅▆▇█"
				return rune(bars[(dx-startX+3)%len(bars)]), greenMid, true
			}
		}

		// Leader star
		if nd.leader && dy == -2 && dx == 0 {
			return '★', theme.Error, true
		}

		// Aura: brighten rain near running bots
		if dx*dx+dy*dy <= 4 {
			if nd.status == bot.StatusRunning {
				return randomGlyph(), greenMid, true
			}
		}
	}

	// Connection lines between parent-child
	for i := range nodes {
		if nodes[i].parent == "" {
			continue
		}
		pi := -1
		for j := range nodes {
			if nodes[j].name == nodes[i].parent {
				pi = j
				break
			}
		}
		if pi < 0 {
			continue
		}
		if onLine(col, row, nodes[pi].x, nodes[pi].y, nodes[i].x, nodes[i].y) {
			dist := abs(col-nodes[pi].x) + abs(row-nodes[pi].y)
			ch := randomGlyph()
			step := (dist + v.frame/2) % 6
			if step == 0 {
				return ch, glowFill, true
			}
			return ch, glowEdge, true
		}
	}

	return 0, 0, false
}

func onLine(px, py, x1, y1, x2, y2 int) bool {
	dx, dy := x2-x1, y2-y1
	len := abs(dx) + abs(dy)
	if len == 0 {
		return false
	}
	for s := 0; s <= len; s++ {
		if px == x1+dx*s/len && py == y1+dy*s/len {
			return true
		}
	}
	return false
}

func randomGlyph() rune {
	return matrixGlyphs[rand.Intn(len(matrixGlyphs))]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (d *Dashboard) logAvatarErr(bot, format string, args ...any) {
	msg := fmt.Sprintf("avatar "+format, args...)
	d.log.Error(msg, "bot", bot)
	logDir := filepath.Join(filepath.Dir(d.mgr.BotsDir), ".praxis")
	_ = os.MkdirAll(logDir, 0o755)
	f, err := os.OpenFile(filepath.Join(logDir, "avatar.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		_, _ = fmt.Fprintf(f, "%s [%s] %s\n", time.Now().Format(time.RFC3339), bot, msg)
		_ = f.Close()
	}
}

func (d *Dashboard) generateAvatar(name, goal string) {
	avatarPath := filepath.Join(d.mgr.BotDir(name), "avatar.txt")
	if _, err := os.Stat(avatarPath); err == nil {
		return
	}

	spec := faceSpecs[rand.Intn(len(faceSpecs))]
	lines := spec.render()
	data := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(avatarPath, []byte(data), 0o644); err != nil {
		d.logAvatarErr(name, "write failed: %v", err)
	}
}

const maskW = 15

type faceSpec struct {
	halfWidths   []float64
	eyeY         int
	eyeXOff      float64
	eyeRX, eyeRY float64
	mouthY       int
	mouthHW      float64
	extras       string
}

var faceSpecs = []faceSpec{
	{
		halfWidths: []float64{4, 5.5, 6.5, 7, 6.5, 5.5, 4, 2},
		eyeY: 2, eyeXOff: 2.5, eyeRX: 1, eyeRY: 0.8,
		mouthY: 5, mouthHW: 2,
	},
	{
		halfWidths: []float64{0.5, 5, 6.5, 7, 7, 6.5, 5, 3},
		eyeY: 3, eyeXOff: 2.5, eyeRX: 1.5, eyeRY: 0.5,
		mouthY: 5, mouthHW: 3, extras: "antenna",
	},
	{
		halfWidths: []float64{5, 6.5, 7, 6, 5, 3.5, 2, 1},
		eyeY: 2, eyeXOff: 2.2, eyeRX: 1.5, eyeRY: 1.2,
		mouthY: 5, mouthHW: 1,
	},
	{
		halfWidths: []float64{3.5, 5.5, 7, 7, 6.5, 5, 3, 1},
		eyeY: 3, eyeXOff: 2.5, eyeRX: 0.7, eyeRY: 1,
		mouthY: 5, mouthHW: 0.5, extras: "ears",
	},
	{
		halfWidths: []float64{0.5, 5, 6.5, 7, 7, 6, 4, 2.5},
		eyeY: 3, eyeXOff: 3, eyeRX: 2, eyeRY: 0.4,
		mouthY: -1, mouthHW: 0, extras: "visor",
	},
	{
		halfWidths: []float64{4.5, 6, 6.5, 6.5, 6, 4.5, 3, 1.5},
		eyeY: 2, eyeXOff: 2.2, eyeRX: 1.2, eyeRY: 1,
		mouthY: 5, mouthHW: 2, extras: "teeth",
	},
}

func (s faceSpec) render() []string {
	cx := float64(maskW / 2)
	cyF := float64(len(s.halfWidths)) / 2
	h := len(s.halfWidths)

	maxHW := 0.0
	for _, w := range s.halfWidths {
		if w > maxHW {
			maxHW = w
		}
	}

	hwAt := func(fy float64) float64 {
		n := float64(len(s.halfWidths) - 1)
		if fy < 0 {
			return s.halfWidths[0] * math.Max(0, 1+fy)
		}
		if fy >= n {
			return s.halfWidths[len(s.halfWidths)-1] * math.Max(0, 1-(fy-n))
		}
		y0 := int(fy)
		if y0 >= len(s.halfWidths)-1 {
			y0 = len(s.halfWidths) - 2
		}
		frac := fy - float64(y0)
		return s.halfWidths[y0]*(1-frac) + s.halfWidths[y0+1]*frac
	}

	grid := make([][]byte, h)
	for y := range grid {
		grid[y] = make([]byte, maskW)
		for x := range grid[y] {
			grid[y][x] = '0'
		}
	}

	for y := 0; y < h; y++ {
		sy := y + 1
		if sy >= h {
			continue
		}
		for x := 0; x < maskW; x++ {
			sx := x + 1
			if sx >= maskW {
				continue
			}
			dx := math.Abs(float64(x) - cx)
			hw := hwAt(float64(y))
			if hw > 0 && dx <= hw {
				grid[sy][sx] = '1'
			}
		}
	}

	for y := 0; y < h; y++ {
		fy := float64(y)
		for x := 0; x < maskW; x++ {
			fx := float64(x)
			dx := math.Abs(fx - cx)

			hw := hwAt(fy)
			hwU := hwAt(fy - 0.5)
			hwD := hwAt(fy + 0.5)
			dist := hw - dx
			if d := hwU - dx; d > dist {
				dist = d
			}
			if d := hwD - dx; d > dist {
				dist = d
			}

			switch {
			case dist > 2.5:
				lx := (fx - cx) / maxHW
				ly := (fy - cyF) / float64(h)
				light := -lx*0.6 - ly*0.5
				if light > 0.25 {
					grid[y][x] = '3'
				} else {
					grid[y][x] = '2'
				}
			case dist > 1:
				grid[y][x] = '2'
			case dist > -0.5:
				grid[y][x] = '1'
			case dist > -2.5:
				if grid[y][x] == '0' {
					grid[y][x] = '1'
				}
			}

			if s.eyeRX > 0 {
				eyF := float64(s.eyeY)
				for _, sign := range []float64{-1, 1} {
					ex := cx + sign*s.eyeXOff
					edx := (fx - ex) / s.eyeRX
					edy := (fy - eyF) / s.eyeRY
					ed := edx*edx + edy*edy
					if ed <= 1.0 {
						if ed < 0.2 {
							grid[y][x] = '4'
						} else {
							grid[y][x] = '3'
						}
					}
				}
			}

			if s.mouthHW > 0 && s.mouthY >= 0 && s.mouthY < h {
				myF := float64(s.mouthY)
				mdx := (fx - cx) / s.mouthHW
				mdy := fy - myF
				if mdx*mdx <= 1.0 && mdy >= -0.45 && mdy <= 0.45 {
					grid[y][x] = '3'
				}
			}
		}
	}

	switch s.extras {
	case "antenna":
		mid := maskW / 2
		grid[0][mid] = '4'
	case "ears":
		grid[0][2] = '2'
		grid[0][maskW-3] = '2'
		grid[1][1] = '1'
		grid[1][maskW-2] = '1'
	case "visor":
		for y := s.eyeY; y <= s.eyeY+int(math.Ceil(s.eyeRY)); y++ {
			if y >= 0 && y < h {
				for x := 0; x < maskW; x++ {
					if grid[y][x] != '0' {
						grid[y][x] = '4'
					}
				}
			}
		}
	case "teeth":
		if s.mouthY >= 0 && s.mouthY < h {
			for x := 0; x < maskW; x++ {
				if grid[s.mouthY][x] == '3' && x%2 == 0 {
					grid[s.mouthY][x] = '4'
				}
			}
		}
	}

	lines := make([]string, h)
	for y := range grid {
		lines[y] = string(grid[y])
	}
	return lines
}

func avatarGlyph(name string, row, col, frame int) rune {
	h := uint32(0)
	for _, c := range name {
		h = h*31 + uint32(c)
	}
	h += uint32(row)*17 + uint32(col)*13
	phase := h % 13
	step := uint32((frame + int(phase)) / 5)
	h += step * 7
	return matrixGlyphs[int(h)%len(matrixGlyphs)]
}

func brightnessColor(b byte) gotui.Color {
	switch b {
	case '1':
		return greenDarkest
	case '2':
		return greenDark
	case '3':
		return greenDim
	case '4':
		return greenBright
	default:
		return greenMid
	}
}

func loadAvatar(botDir string) []string {
	data, err := os.ReadFile(filepath.Join(botDir, "avatar.txt"))
	if err != nil {
		return nil
	}
	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		l = strings.TrimRight(l, "\r ")
		if l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) >= 5 {
		return lines
	}
	return nil
}

func (d *Dashboard) cmdWorkspace(args string) {
	parts := strings.Fields(args)
	if len(parts) == 0 {
		d.showInfo("usage: /workspace list|add|remove")
		return
	}
	switch parts[0] {
	case "list":
		d.wsList()
	case "add":
		d.wsAdd(parts[1:])
	case "remove":
		d.wsRemove(parts[1:])
	default:
		d.showInfo("usage: /workspace list|add|remove")
	}
}

func (d *Dashboard) wsList() {
	projectDir := filepath.Dir(d.mgr.BotsDir)
	data, err := os.ReadFile(filepath.Join(projectDir, "workspaces.json"))
	if err != nil {
		d.showInfo("no workspaces.json found")
		return
	}
	var workspaces map[string]interface{}
	if err := json.Unmarshal(data, &workspaces); err != nil {
		d.showInfo(fmt.Sprintf("parse error: %v", err))
		return
	}

	main := d.ui.Panel("main")
	theme := d.ui.Theme()
	var sb strings.Builder
	sb.WriteString(main.Styled(theme.Primary, "━━━ workspaces ━━━") + "\n\n")

	// Count bots per workspace
	bots, _ := d.mgr.List()
	wsBots := make(map[string][]string)
	for _, b := range bots {
		w := b.Config.Workspace
		if w == "" {
			w = "(none)"
		}
		wsBots[w] = append(wsBots[w], b.Config.Name)
	}

	if len(workspaces) == 0 {
		sb.WriteString("  (none configured)\n")
	}
	for name, entry := range workspaces {
		fmt.Fprintf(&sb, "  %s\n", main.Styled(theme.Primary, name))
		switch v := entry.(type) {
		case string:
			fmt.Fprintf(&sb, "    path: %s\n", v)
		case map[string]interface{}:
			if p, ok := v["path"].(string); ok {
				fmt.Fprintf(&sb, "    path: %s\n", p)
			}
			if s, ok := v["gossip_secret"].(string); ok {
				fmt.Fprintf(&sb, "    secret: %s\n", maskSecret(s))
			}
			if ds, ok := v["default_scope"].(string); ok {
				fmt.Fprintf(&sb, "    scope: %s\n", ds)
			}
		}
		if bots, ok := wsBots[name]; ok {
			fmt.Fprintf(&sb, "    bots: %d (%s)\n", len(bots), strings.Join(bots, ", "))
		}
	}
	main.WriteString(sb.String())
}

func (d *Dashboard) wsAdd(parts []string) {
	if len(parts) < 2 {
		d.showInfo("usage: /workspace add <name> <path> [gossip_secret=<s>] [scope=<s>]")
		return
	}
	name := parts[0]
	wsPath := parts[1]
	opts := map[string]string{}
	for _, kv := range parts[2:] {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			opts[kv[:idx]] = kv[idx+1:]
		}
	}

	projectDir := filepath.Dir(d.mgr.BotsDir)
	wsFile := filepath.Join(projectDir, "workspaces.json")

	workspaces := make(map[string]interface{})
	if data, err := os.ReadFile(wsFile); err == nil {
		_ = json.Unmarshal(data, &workspaces)
	}

	entry := map[string]interface{}{
		"path": wsPath,
	}
	if s, ok := opts["gossip_secret"]; ok {
		entry["gossip_secret"] = s
	}
	if s, ok := opts["scope"]; ok {
		entry["default_scope"] = s
	}
	workspaces[name] = entry

	if err := writeJSON(wsFile, workspaces); err != nil {
		d.showInfo(fmt.Sprintf("write error: %v", err))
		return
	}
	d.showInfo(fmt.Sprintf("added workspace %s → %s", name, wsPath))
}

func (d *Dashboard) wsRemove(parts []string) {
	if len(parts) < 1 {
		d.showInfo("usage: /workspace remove <name>")
		return
	}
	name := parts[0]

	// Check if any bots use this workspace
	bots, _ := d.mgr.List()
	for _, b := range bots {
		if b.Config.Workspace == name {
			d.showInfo(fmt.Sprintf("workspace %s is in use by bot %s", name, b.Config.Name))
			return
		}
	}

	projectDir := filepath.Dir(d.mgr.BotsDir)
	wsFile := filepath.Join(projectDir, "workspaces.json")

	workspaces := make(map[string]interface{})
	if data, err := os.ReadFile(wsFile); err == nil {
		_ = json.Unmarshal(data, &workspaces)
	}

	if _, ok := workspaces[name]; !ok {
		d.showInfo(fmt.Sprintf("workspace %s not found", name))
		return
	}
	delete(workspaces, name)

	if err := writeJSON(wsFile, workspaces); err != nil {
		d.showInfo(fmt.Sprintf("write error: %v", err))
		return
	}
	d.showInfo(fmt.Sprintf("removed workspace %s", name))
}

func (d *Dashboard) resolveWorkspace(name string) (path, gossipSecret, defaultScope string, err error) {
	projectDir := filepath.Dir(d.mgr.BotsDir)
	data, readErr := os.ReadFile(filepath.Join(projectDir, "workspaces.json"))
	if readErr != nil {
		return "", "", "", fmt.Errorf("workspaces.json not found")
	}
	var workspaces map[string]interface{}
	if jsonErr := json.Unmarshal(data, &workspaces); jsonErr != nil {
		return "", "", "", fmt.Errorf("workspaces.json parse error: %v", jsonErr)
	}
	entry, ok := workspaces[name]
	if !ok {
		return "", "", "", fmt.Errorf("workspace %q not found", name)
	}
	switch v := entry.(type) {
	case string:
		path = v
	case map[string]interface{}:
		if p, ok := v["path"].(string); ok {
			path = p
		}
		if s, ok := v["gossip_secret"].(string); ok {
			gossipSecret = s
		}
		if ds, ok := v["default_scope"].(string); ok {
			defaultScope = ds
		}
	}
	if path == "" {
		return "", "", "", fmt.Errorf("workspace %q has no path", name)
	}
	return path, gossipSecret, defaultScope, nil
}

func (d *Dashboard) showInfo(msg string) {
	if d.vizActive {
		d.ui.AddMessage(gotui.RoleSystem, "["+msg+"]")
		return
	}
	d.ui.Panel("main").WriteString("[" + msg + "]\n")
}

func staleThreshold() time.Duration {
	return time.Duration(envInt("BOT_STALE_THRESHOLD", 120)) * time.Second
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func readLastN(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("empty")
	}
	return strings.Join(lines, "\n") + "\n", scanner.Err()
}

func parseSpawnArgs(args string) (name, goal string, opts map[string]string) {
	opts = make(map[string]string)
	args = strings.TrimSpace(args)
	if args == "" {
		return
	}
	parts := strings.Fields(args)
	if len(parts) < 2 {
		return
	}
	name = parts[0]

	rest := strings.TrimSpace(args[len(name):])
	if strings.HasPrefix(rest, `"`) {
		end := strings.Index(rest[1:], `"`)
		if end < 0 {
			return
		}
		goal = rest[1 : end+1]
		rest = strings.TrimSpace(rest[end+2:])
	} else {
		goal = parts[1]
		rest = strings.TrimSpace(strings.Join(parts[2:], " "))
	}

	for _, kv := range strings.Fields(rest) {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			opts[kv[:idx]] = kv[idx+1:]
		}
	}
	return
}

func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[:2] + strings.Repeat("*", len(s)-4) + s[len(s)-2:]
}

func sanitizeModel(m string) string {
	m = strings.ReplaceAll(m, "/", "_")
	m = strings.ReplaceAll(m, ":", "_")
	m = strings.ReplaceAll(m, ".", "_")
	return m
}

func countQueueTickets(queueDir string) int {
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".wait") {
			count++
		}
	}
	return count
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
