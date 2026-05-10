package tui

import (
	"bufio"
	"context"
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
	"praxis/internal/config"
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
	cfg  *config.Config

	mu          sync.Mutex
	selectedBot string
	logCancel   context.CancelFunc
	logOffset   int64
	vizActive   bool

	botNameCmds []*gotui.Command

	quitMu      sync.Mutex
	quitPending bool
	quitCancel  context.CancelFunc
}

func New(mgr *bot.Manager, pool *bot.RunnerPool, node *cluster.Node, sb sandbox.Sandbox, log logger.Logger, cfg *config.Config) *Dashboard {
	d := &Dashboard{
		mgr:  mgr,
		pool: pool,
		node: node,
		sb:   sb,
		log:  log,
		cfg:  cfg,
	}

	themeNames := gotui.ThemeNames()
	themeArgs := make([]string, len(themeNames))
	copy(themeArgs, themeNames)

	var botNameCmds []*gotui.Command

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
			botCmd("select", "Switch log view to a bot", func(args string) { d.cmdSelect(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "list", Description: "List all bots with details", Handler: func(_ string) { d.cmdList() }},
			botCmd("info", "Show full bot config/status [bot]", func(args string) { d.cmdInfo(strings.TrimSpace(args)) }, &botNameCmds),
			botCmd("logs", "Show recent log lines [bot] [lines]", func(args string) { d.cmdLogs(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "top", Description: "Scroll log panel to top", Handler: func(_ string) { d.ui.Panel("main").ScrollToTop() }},
			botCmd("start", "Start a bot [bot] [model=...] [thinking=true|false] [goal=...] [scope=...] [msg]", func(args string) { d.cmdStartWithMessage(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "start-all", Description: "Start all stopped bots", Handler: func(_ string) { d.cmdStartAll() }},
			botCmd("stop", "Stop a bot gracefully [bot]", func(args string) { d.cmdStop(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "stop-all", Description: "Stop all running bots", Handler: func(_ string) { d.cmdStopAll() }},
			botCmd("kill", "Kill a bot immediately [bot]", func(args string) { d.cmdKill(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "kill-all", Description: "Kill all running bots", Handler: func(_ string) { d.cmdKillAll() }},
			botCmd("restart", "Kill and restart a bot [bot] [model=...] [thinking=true|false] [goal=...] [scope=...] [msg]", func(args string) { d.cmdRestartWithConfig(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "restart-stale", Description: "Restart all stale bots", Handler: func(_ string) { d.cmdRestartStale() }},
			botCmd("refresh", "Update bot.py from current template [bot]", func(args string) { d.cmdRefresh(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "refresh-all", Description: "Update all bots bot.py from current template", Handler: func(_ string) { d.cmdRefreshAll() }},
			botCmd("remove", "Kill and permanently delete a bot", func(args string) { d.cmdRemove(strings.TrimSpace(args)) }, &botNameCmds),
			botCmd("send", "Send a message to a bot <bot> <msg>", func(args string) { d.cmdSend(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "spawn", Description: `Spawn a new bot <name> "<goal>" [model=<m>] [workspace=<w>] [scope=<s>] [thinking=<true|false>] [node=<n>]`, Handler: func(args string) { d.cmdSpawn(strings.TrimSpace(args)) }},
			{Name: "nodes", Description: "List watchdog nodes in the cluster", Handler: func(_ string) { d.cmdNodes() }},
			botCmd("export", "Export a bot archive <bot> [path]", func(args string) { d.cmdExport(strings.TrimSpace(args)) }, &botNameCmds),
			{Name: "workspace", Description: "Manage workspaces: list|add|remove", Handler: func(args string) { d.cmdWorkspace(strings.TrimSpace(args)) }},
			{Name: "theme", Description: "Switch colour theme", Args: themeArgs, Handler: func(args string) { d.cmdTheme(strings.TrimSpace(args)) }},
			{Name: "visualise", Description: "Matrix-style swarm visualisation", Handler: func(_ string) { d.cmdVisualise() }},
			{Name: "exit", Description: "Exit the TUI", Handler: func(_ string) { d.ui.Exit() }},
		},
	})

	d.botNameCmds = botNameCmds

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

	d.refreshBotNameArgs()

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
		peerCount := len(gc.AliveNodes())
		fmt.Fprintf(&sb, "peers: %d\n", peerCount-1) // exclude self
	}

	d.botPanel.SetTitle(fmt.Sprintf("BOTS %d/%d", alive, len(bots)))
	d.botPanel.SetContent(sb.String())
}

func (d *Dashboard) refreshDetailPanel() {
	theme := d.ui.Theme()
	var sb strings.Builder

	bots, _ := d.mgr.List()

	// Model usage + queue stats (from config + actual bots)
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

	if d.cfg != nil {
		for _, m := range d.cfg.Models.Catalog {
			if m.ID == "" {
				continue
			}
			mi := &modelInfo{}
			if m.Label != "" {
				mi.Label = m.Label
			}
			if m.Concurrency > 0 {
				mi.Concurrency = m.Concurrency
			}
			models[m.ID] = mi
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

	knownBots := make(map[string]bool, len(bots))
	for _, b := range bots {
		knownBots[b.Config.Name] = true
	}
	cleanStaleQueueTickets(d.mgr.LocksDir, knownBots)

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
			if queueCount := countQueueTickets(queueDir, knownBots); queueCount > 0 {
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

	if wsConfig := d.cfg; wsConfig != nil {
		for _, ws := range wsConfig.Workspaces {
			name := ws.Name
			if _, ok := wsMap[name]; !ok {
				wsMap[name] = &wsInfo{}
			}
			wsMap[name].Path = ws.Path
			if ws.Scope != "" {
				wsMap[name].Scope = ws.Scope
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

	if d.node != nil {
		var devices []string
		seen := make(map[string]bool)
		for _, gn := range d.node.Cluster().AliveNodes() {
			if gn.Metadata.GetString("role") == "device" {
				name := gn.Metadata.GetString("id")
				if name == "" {
					name = gn.ID.String()[:8]
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				devices = append(devices, name)
			}
		}
		if len(devices) > 0 {
			sb.WriteString("\n" + d.detailPanel.Styled(theme.Primary, "━━━ devices ━━━") + "\n\n")
			for _, name := range devices {
				fmt.Fprintf(&sb, " %s\n", d.detailPanel.Styled(theme.Text, name))
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

func (d *Dashboard) applyConfigAndRefresh(name string, args string) ([]string, string, bool) {
	remaining, configUpdates := parseKeyValueArgs(args)

	refresh := configUpdates["refresh"] == "true"
	delete(configUpdates, "refresh")

	var info []string
	if refresh {
		if err := d.mgr.RefreshTemplate(name); err != nil {
			d.showInfo(fmt.Sprintf("refresh %s: %v", name, err))
			return nil, "", false
		}
		info = append(info, "refreshed")
	}
	if len(configUpdates) > 0 {
		if err := d.mgr.UpdateConfig(name, configUpdates); err != nil {
			d.showInfo(fmt.Sprintf("config update: %v", err))
			return nil, "", false
		}
		keys := make([]string, 0, len(configUpdates))
		for k := range configUpdates {
			keys = append(keys, k+"="+configUpdates[k])
		}
		info = append(info, "config: "+strings.Join(keys, ", "))
	}
	return info, remaining, true
}

func (d *Dashboard) cmdStartWithMessage(args string) {
	var remaining string
	var name string
	{
		d.mu.Lock()
		selected := d.selectedBot
		d.mu.Unlock()
		remaining, name = extractBotName(args, selected)
	}
	if name == "" {
		d.showInfo("usage: /start <bot> [key=value ...] [message]")
		return
	}
	b, err := d.mgr.Get(name)
	if err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}

	info, remaining, ok := d.applyConfigAndRefresh(name, remaining)
	if !ok {
		return
	}

	switch b.State.Status {
	case bot.StatusRunning, bot.StatusStarting:
		if len(info) > 0 {
			info = append(info, "/restart to apply")
		} else {
			info = append(info, fmt.Sprintf("%s is already %s", name, b.State.Status))
		}
	default:
		if err := d.pool.Start(name); err != nil {
			d.showInfo(fmt.Sprintf("start %s: %v", name, err))
			return
		}
		info = append(info, "started")
	}

	if remaining != "" {
		info = append(info, "msg: "+remaining)
	}
	d.showInfo(name + " · " + strings.Join(info, " | "))

	if remaining != "" {
		d.ui.AddMessage(gotui.RoleUser, remaining)
		if err := d.node.SendMessage(name, remaining); err != nil {
			main := d.ui.Panel("main")
			main.WriteString(main.Styled(d.ui.Theme().Error, "[send failed: "+err.Error()+"]") + "\n")
			return
		}
		d.ui.AddMessageAs(gotui.RoleAssistant, name, "message sent")
	}

	if !d.vizActive {
		d.selectBot(d.ui.Context(), name)
	}
}

func parseKeyValueArgs(args string) (remaining string, kv map[string]string) {
	kv = map[string]string{}
	var rest []string
	i := 0
	for i < len(args) {
		for i < len(args) && args[i] == ' ' {
			i++
		}
		if i >= len(args) {
			break
		}
		eq := strings.IndexByte(args[i:], '=')
		if eq < 0 {
			j := strings.IndexByte(args[i:], ' ')
			if j < 0 {
				rest = append(rest, args[i:])
				break
			}
			rest = append(rest, args[i:i+j])
			i += j + 1
			continue
		}
		key := args[i : i+eq]
		if strings.Contains(key, " ") || key == "" {
			rest = append(rest, args[i:])
			break
		}
		i += eq + 1
		if i >= len(args) {
			kv[key] = ""
			break
		}
		var val string
		if args[i] == '"' {
			end := strings.IndexByte(args[i+1:], '"')
			if end >= 0 {
				val = args[i+1 : i+1+end]
				i += end + 2
			} else {
				j := strings.IndexByte(args[i:], ' ')
				if j < 0 {
					val = args[i+1:]
					i = len(args)
				} else {
					val = args[i+1 : i+j]
					i += j + 1
				}
			}
		} else {
			j := strings.IndexByte(args[i:], ' ')
			if j < 0 {
				val = args[i:]
				i = len(args)
			} else {
				val = args[i : i+j]
				i += j + 1
			}
		}
		kv[key] = val
	}
	return strings.Join(rest, " "), kv
}

func extractBotName(args string, selected string) (remaining string, name string) {
	args = strings.TrimSpace(args)
	if args == "" {
		return "", selected
	}
	first := strings.SplitN(args, " ", 2)[0]
	if err := bot.ValidateName(first); err == nil {
		if len(args) > len(first) {
			return args[len(first)+1:], first
		}
		return "", first
	}
	return args, selected
}

func botCmd(name, desc string, handler func(string), list *[]*gotui.Command) *gotui.Command {
	cmd := &gotui.Command{Name: name, Description: desc, Handler: handler}
	*list = append(*list, cmd)
	return cmd
}

func (d *Dashboard) refreshBotNameArgs() {
	bots, err := d.mgr.List()
	if err != nil {
		return
	}
	names := make([]string, len(bots))
	for i, b := range bots {
		names[i] = b.Config.Name
	}
	for _, cmd := range d.botNameCmds {
		cmd.Args = names
	}
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
		default:
			if err := d.pool.Start(b.Config.Name); err != nil {
				d.showInfo(fmt.Sprintf("start %s: %v", b.Config.Name, err))
				continue
			}
			started++
		}
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

func (d *Dashboard) cmdRefresh(name string) {
	if name == "" {
		d.mu.Lock()
		name = d.selectedBot
		d.mu.Unlock()
	}
	if name == "" {
		d.showInfo("usage: /refresh <bot>")
		return
	}
	if _, err := d.mgr.Get(name); err != nil {
		d.showInfo(fmt.Sprintf("unknown bot: %s", name))
		return
	}
	if err := d.mgr.RefreshTemplate(name); err != nil {
		d.showInfo(fmt.Sprintf("refresh %s: %v", name, err))
		return
	}
	hint := ""
	if b, _ := d.mgr.Get(name); b != nil && (b.State.Status == bot.StatusRunning || b.State.Status == bot.StatusStarting) {
		hint = " — /restart to apply"
	}
	d.showInfo("refreshed bot.py for " + name + hint)
}

func (d *Dashboard) cmdRefreshAll() {
	bots, err := d.mgr.List()
	if err != nil {
		d.showInfo(fmt.Sprintf("error: %v", err))
		return
	}
	refreshed, failed := 0, 0
	for _, b := range bots {
		if err := d.mgr.RefreshTemplate(b.Config.Name); err != nil {
			failed++
		} else {
			refreshed++
		}
	}
	msg := fmt.Sprintf("refreshed %d bots", refreshed)
	if failed > 0 {
		msg += fmt.Sprintf(", %d failed", failed)
	}
	d.showInfo(msg)
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

func (d *Dashboard) cmdRestartWithConfig(args string) {
	var remaining string
	var name string
	{
		d.mu.Lock()
		selected := d.selectedBot
		d.mu.Unlock()
		remaining, name = extractBotName(args, selected)
	}
	if name == "" {
		d.showInfo("usage: /restart <bot> [key=value ...] [message]")
		return
	}

	info, remaining, ok := d.applyConfigAndRefresh(name, remaining)
	if !ok {
		return
	}

	_ = d.pool.Kill(name)
	time.Sleep(300 * time.Millisecond)
	if err := d.pool.Start(name); err != nil {
		d.showInfo(fmt.Sprintf("restart %s: %v", name, err))
		return
	}
	info = append(info, "restarted")

	if remaining != "" {
		info = append(info, "msg: "+remaining)
		d.ui.AddMessage(gotui.RoleUser, remaining)
		if err := d.node.SendMessage(name, remaining); err != nil {
			main := d.ui.Panel("main")
			main.WriteString(main.Styled(d.ui.Theme().Error, "[send failed: "+err.Error()+"]") + "\n")
		} else {
			d.ui.AddMessageAs(gotui.RoleAssistant, name, "message sent")
		}
	}

	d.showInfo(name + " · " + strings.Join(info, " | "))
	if !d.vizActive {
		d.selectBot(d.ui.Context(), name)
	}
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
	if err := d.pool.Kill(name); err != nil && !strings.Contains(err.Error(), "not running") {
		d.showInfo(fmt.Sprintf("kill %s: %v", name, err))
		return
	}
	d.mgr.RemoveLocks(name)
	if err := d.mgr.Delete(name); err != nil {
		return
	}
	d.mu.Lock()
	if d.selectedBot == name {
		d.selectedBot = ""
		if d.logCancel != nil {
			d.logCancel()
			d.logCancel = nil
		}
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
		d.showInfo(`usage: /spawn <name> "<goal>" [model=<m>] [workspace=<w>] [scope=<s>] [thinking=<true|false>] [node=<n>]`)
		return
	}
	model := opts["model"]
	if model == "" && d.cfg != nil {
		model = d.cfg.Bot.Model
	}
	thinking := true
	if v, ok := opts["thinking"]; ok {
		thinking = v != "false"
	}
	cfg := &bot.BotConfig{
		Name:     name,
		Goal:     goal,
		Model:    model,
		Thinking: thinking,
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

	if nodeName := opts["node"]; nodeName != "" {
		if d.node == nil {
			d.showInfo("cluster not available — cannot remote spawn")
			return
		}
		botID, err := d.node.SpawnRemote(nodeName, cfg)
		if err != nil {
			d.showInfo(fmt.Sprintf("remote spawn error: %v", err))
			return
		}
		d.showInfo(fmt.Sprintf("spawned %s on %s (id: %s)", name, nodeName, botID))
		return
	}

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

func (d *Dashboard) cmdNodes() {
	if d.node == nil {
		d.showInfo("cluster not available")
		return
	}
	names := d.node.ListWatchdogNodes()
	if len(names) == 0 {
		d.showInfo("no remote watchdog nodes in cluster")
		return
	}
	d.showInfo("watchdog nodes: " + strings.Join(names, ", "))
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
	d         *Dashboard
	panel     *gotui.Panel
	drops     []drop
	frame     int
	pendingMu sync.Mutex
	pending   map[string]bool
}

type drop struct {
	col   int
	y     float64
	speed float64
	len   int
	chars []rune
}

type botNode struct {
	name   string
	status string
	ticks  int64
	x, y   int
	parent string
	leader bool
	stale  bool
	avatar []string
	dir    string
	active bool
}

type maskInfo struct {
	mask  byte
	node  int
	avRow int
	avCol int
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
		eyeY:       2, eyeXOff: 2.5, eyeRX: 1, eyeRY: 0.8,
		mouthY: 5, mouthHW: 2,
	},
	{
		halfWidths: []float64{0.5, 5, 6.5, 7, 7, 6.5, 5, 3},
		eyeY:       3, eyeXOff: 2.5, eyeRX: 1.5, eyeRY: 0.5,
		mouthY: 5, mouthHW: 3, extras: "antenna",
	},
	{
		halfWidths: []float64{5, 6.5, 7, 6, 5, 3.5, 2, 1},
		eyeY:       2, eyeXOff: 2.2, eyeRX: 1.5, eyeRY: 1.2,
		mouthY: 5, mouthHW: 1,
	},
	{
		halfWidths: []float64{3.5, 5.5, 7, 7, 6.5, 5, 3, 1},
		eyeY:       3, eyeXOff: 2.5, eyeRX: 0.7, eyeRY: 1,
		mouthY: 5, mouthHW: 0.5, extras: "ears",
	},
	{
		halfWidths: []float64{0.5, 5, 6.5, 7, 7, 6, 4, 2.5},
		eyeY:       3, eyeXOff: 3, eyeRX: 2, eyeRY: 0.4,
		mouthY: -1, mouthHW: 0, extras: "visor",
	},
	{
		halfWidths: []float64{4.5, 6, 6.5, 6.5, 6, 4.5, 3, 1.5},
		eyeY:       2, eyeXOff: 2.2, eyeRX: 1.2, eyeRY: 1,
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
	if d.cfg == nil || len(d.cfg.Workspaces) == 0 {
		d.showInfo("no workspaces configured")
		return
	}

	main := d.ui.Panel("main")
	theme := d.ui.Theme()
	var sb strings.Builder
	sb.WriteString(main.Styled(theme.Primary, "━━━ workspaces ━━━") + "\n\n")

	bots, _ := d.mgr.List()
	wsBots := make(map[string][]string)
	for _, b := range bots {
		w := b.Config.Workspace
		if w == "" {
			w = "(none)"
		}
		wsBots[w] = append(wsBots[w], b.Config.Name)
	}

	if len(d.cfg.Workspaces) == 0 {
		sb.WriteString("  (none configured)\n")
	}
	for _, ws := range d.cfg.Workspaces {
		fmt.Fprintf(&sb, "  %s\n", main.Styled(theme.Primary, ws.Name))
		fmt.Fprintf(&sb, "    path: %s\n", ws.Path)
		if ws.Secret != "" {
			fmt.Fprintf(&sb, "    secret: %s\n", maskSecret(ws.Secret))
		}
		if ws.Scope != "" {
			fmt.Fprintf(&sb, "    scope: %s\n", ws.Scope)
		}
		if bots, ok := wsBots[ws.Name]; ok {
			fmt.Fprintf(&sb, "    bots: %d (%s)\n", len(bots), strings.Join(bots, ", "))
		}
	}
	main.WriteString(sb.String())
}

func (d *Dashboard) wsAdd(parts []string) {
	if len(parts) < 2 {
		d.showInfo("usage: /workspace add <name> <path> [secret=<s>] [scope=<s>]")
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

	entry := config.WorkspaceEntry{
		Name: name,
		Path: wsPath,
	}
	if s, ok := opts["secret"]; ok {
		entry.Secret = s
	}
	if s, ok := opts["scope"]; ok {
		entry.Scope = s
	}

	d.cfg.SetWorkspace(entry)

	projectDir := filepath.Dir(d.mgr.BotsDir)
	if err := d.cfg.Save(projectDir); err != nil {
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

	bots, _ := d.mgr.List()
	for _, b := range bots {
		if b.Config.Workspace == name {
			d.showInfo(fmt.Sprintf("workspace %s is in use by bot %s", name, b.Config.Name))
			return
		}
	}

	if !d.cfg.RemoveWorkspace(name) {
		d.showInfo(fmt.Sprintf("workspace %s not found", name))
		return
	}

	projectDir := filepath.Dir(d.mgr.BotsDir)
	if err := d.cfg.Save(projectDir); err != nil {
		d.showInfo(fmt.Sprintf("write error: %v", err))
		return
	}
	d.showInfo(fmt.Sprintf("removed workspace %s", name))
}

func (d *Dashboard) resolveWorkspace(name string) (path, gossipSecret, defaultScope string, err error) {
	if d.cfg == nil {
		return "", "", "", fmt.Errorf("config not loaded")
	}
	p, s, sc, ok := d.cfg.ResolveWorkspace(name)
	if !ok {
		return "", "", "", fmt.Errorf("workspace %q not found", name)
	}
	if p == "" {
		return "", "", "", fmt.Errorf("workspace %q has no path", name)
	}
	return p, s, sc, nil
}

func (d *Dashboard) showInfo(msg string) {
	if d.vizActive {
		d.ui.AddMessage(gotui.RoleSystem, "["+msg+"]")
		return
	}
	d.ui.Panel("main").WriteString("[" + msg + "]\n")
}

func staleThreshold() time.Duration {
	cfg := config.Get()
	n := 120
	if cfg != nil && cfg.Bot.StaleThreshold > 0 {
		n = cfg.Bot.StaleThreshold
	}
	return time.Duration(n) * time.Second
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

func cleanStaleQueueTickets(locksDir string, knownBots map[string]bool) {
	entries, err := os.ReadDir(locksDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(locksDir, e.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".wait") {
				continue
			}
			isKnown := false
			for bot := range knownBots {
				if strings.Contains(f.Name(), "_"+bot+".wait") {
					isKnown = true
					break
				}
			}
			if !isKnown {
				_ = os.Remove(filepath.Join(locksDir, e.Name(), f.Name()))
			}
		}
	}
}

func countQueueTickets(queueDir string, knownBots map[string]bool) int {
	entries, err := os.ReadDir(queueDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".wait") {
			continue
		}
		if len(knownBots) > 0 {
			for bot := range knownBots {
				if strings.Contains(e.Name(), "_"+bot+".wait") {
					count++
					break
				}
			}
		} else {
			count++
		}
	}
	return count
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
