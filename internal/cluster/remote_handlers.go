package cluster

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paularlott/gossip"

	"praxis/internal/bot"
)

func (n *Node) handleListBotsReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req ListBotsRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return &ListBotsReply{Error: "bad request: " + err.Error()}, nil
	}

	if !n.validAdminSecret(req.Secret) {
		return &ListBotsReply{Error: "invalid secret"}, nil
	}

	bots, err := n.manager.List()
	if err != nil {
		return &ListBotsReply{Error: "list bots: " + err.Error()}, nil
	}

	entries := make([]BotEntry, 0, len(bots))
	for _, b := range bots {
		e := BotEntry{
			Name:     b.Config.Name,
			Status:   b.State.Status,
			Model:    b.Config.Model,
			Goal:     b.Config.Goal,
			Thinking: b.Config.Thinking,
			Running:  n.isBotRunning(b.Config.Name),
			Ticks:    b.State.TicksAlive(),
		}
		entries = append(entries, e)
	}

	return &ListBotsReply{Bots: entries}, nil
}

func (n *Node) handleBotControlReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req BotControlRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return &BotControlReply{Error: "bad request: " + err.Error()}, nil
	}

	if !n.validAdminSecret(req.Secret) {
		return &BotControlReply{Error: "invalid secret"}, nil
	}

	if req.BotID == "" {
		return n.handleBulkControl(&req)
	}

	switch req.Action {
	case "start":
		if err := n.manager.SetStatus(req.BotID, bot.StatusCreated); err != nil {
			return &BotControlReply{Error: "bot not found: " + req.BotID}, nil
		}
		return &BotControlReply{Status: "started"}, nil
	case "stop":
		if err := n.manager.SetStatus(req.BotID, bot.StatusStopping); err != nil {
			return &BotControlReply{Error: "bot not found: " + req.BotID}, nil
		}
		return &BotControlReply{Status: "stopping"}, nil
	case "kill":
		if err := n.manager.SetStatus(req.BotID, bot.StatusKilled); err != nil {
			return &BotControlReply{Error: "bot not found: " + req.BotID}, nil
		}
		return &BotControlReply{Status: "killed"}, nil
	case "restart":
		if err := n.manager.SetStatus(req.BotID, bot.StatusKilled); err != nil {
			return &BotControlReply{Error: "bot not found: " + req.BotID}, nil
		}
		return &BotControlReply{Status: "restarting"}, nil
	case "refresh":
		if err := n.manager.RefreshTemplate(req.BotID); err != nil {
			return &BotControlReply{Error: "refresh: " + err.Error()}, nil
		}
		return &BotControlReply{Status: "refreshed"}, nil
	case "remove":
		_ = n.manager.SetStatus(req.BotID, bot.StatusKilled)
		n.manager.RemoveLocks(req.BotID)
		if err := n.manager.Delete(req.BotID); err != nil {
			return &BotControlReply{Error: "remove: " + err.Error()}, nil
		}
		return &BotControlReply{Status: "removed"}, nil
	default:
		return &BotControlReply{Error: "unknown action: " + req.Action}, nil
	}
}

func (n *Node) handleBulkControl(req *BotControlRequest) (*BotControlReply, error) {
	bots, err := n.manager.List()
	if err != nil {
		return &BotControlReply{Error: "list bots: " + err.Error()}, nil
	}

	acted := 0
	for _, b := range bots {
		switch req.Action {
		case "start":
			if b.State.Status == bot.StatusRunning || b.State.Status == bot.StatusStarting {
				continue
			}
			if err := n.manager.SetStatus(b.Config.Name, bot.StatusCreated); err != nil {
				continue
			}
		case "stop":
			if b.State.Status != bot.StatusRunning && b.State.Status != bot.StatusStarting {
				continue
			}
			if err := n.manager.SetStatus(b.Config.Name, bot.StatusStopping); err != nil {
				continue
			}
		case "kill":
			if err := n.manager.SetStatus(b.Config.Name, bot.StatusKilled); err != nil {
				continue
			}
		default:
			return &BotControlReply{Error: "unsupported bulk action: " + req.Action}, nil
		}
		acted++
	}

	return &BotControlReply{Status: req.Action + "-all", Count: acted}, nil
}

func (n *Node) handleLogsReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req LogsRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return &LogsReply{Error: "bad request: " + err.Error()}, nil
	}

	if !n.validAdminSecret(req.Secret) {
		return &LogsReply{Error: "invalid secret"}, nil
	}

	if req.BotID == "" {
		return &LogsReply{Error: "bot_id is required"}, nil
	}

	botDir := n.manager.BotDir(req.BotID)
	if _, err := os.Stat(botDir); err != nil {
		return &LogsReply{Error: "bot not found: " + req.BotID}, nil
	}

	if req.Lines <= 0 {
		req.Lines = 40
	}

	var sb strings.Builder
	for _, logName := range []string{"bot.log", "output.log"} {
		logPath := filepath.Join(botDir, logName)
		data, err := readLastNFromPath(logPath, req.Lines)
		if err != nil {
			sb.WriteString(fmt.Sprintf("--- %s (empty) ---\n", logName))
		} else {
			sb.WriteString(fmt.Sprintf("--- %s (last %d lines) ---\n", logName, req.Lines))
			sb.WriteString(data)
		}
	}

	return &LogsReply{Content: sb.String()}, nil
}

func (n *Node) isBotRunning(name string) bool {
	if n.cluster == nil {
		return false
	}
	for _, gn := range n.cluster.AliveNodes() {
		if gn.Metadata.GetString("id") == name {
			return true
		}
	}
	return false
}

func readLastNFromPath(path string, n int) (string, error) {
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
