package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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
		sb.WriteString(d.detailPanel.Styled(theme.Primary, "━━━ LLM models ━━━") + "\n\n")
		for _, id := range modelIDs {
			mi := models[id]
			display := id
			if mi.Label != "" {
				display = mi.Label + " (" + id + ")"
			}
			fmt.Fprintf(&sb, "  %s\n", d.detailPanel.Styled(theme.Text, display))

			parts := []string{}
			if mi.Concurrency > 0 {
				parts = append(parts, fmt.Sprintf("limit: %d", mi.Concurrency))
			}
			if len(mi.Bots) > 0 {
				running := 0
				for _, n := range mi.Bots {
					if d.pool.IsRunning(n) {
						running++
					}
				}
				parts = append(parts, fmt.Sprintf("bots: %d  running: %d", len(mi.Bots), running))
			} else {
				parts = append(parts, "bots: 0")
			}
			sanitized := sanitizeModel(id)
			queueDir := filepath.Join(d.mgr.LocksDir, sanitized)
			if queueCount := countQueueTickets(queueDir); queueCount > 0 {
				parts = append(parts, d.detailPanel.Styled(theme.Error, fmt.Sprintf("queued: %d", queueCount)))
			}
			fmt.Fprintf(&sb, "    %s\n", strings.Join(parts, "  "))
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
			fmt.Fprintf(&sb, "  %s\n", d.detailPanel.Styled(theme.Text, name))
			if info.Path != "" {
				fmt.Fprintf(&sb, "    path: %s\n", info.Path)
			}
			if info.Scope != "" {
				fmt.Fprintf(&sb, "    scope: %s\n", info.Scope)
			}
			if len(info.Bots) > 0 {
				fmt.Fprintf(&sb, "    bots: %d\n", len(info.Bots))
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

func (d *Dashboard) onSubmit(text string) {
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
	d.selectBot(d.ui.Context(), name)
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
		go d.pool.Stop(name)
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
		go d.pool.Kill(name)
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
	d.selectBot(d.ui.Context(), name)
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
			go d.pool.Kill(n)
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
	go d.pool.Kill(name)
	if err := d.mgr.Delete(name); err != nil {
		d.showInfo(fmt.Sprintf("remove %s: %v", name, err))
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
	var lines int = 40
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
	d.selectBot(d.ui.Context(), name)
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
	type wsEntry struct {
		Path         string `json:"path"`
		GossipSecret string `json:"gossip_secret,omitempty"`
		DefaultScope string `json:"default_scope,omitempty"`
	}
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
	defer f.Close()
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
