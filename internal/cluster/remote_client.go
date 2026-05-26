package cluster

import (
	"fmt"

	"praxis/internal/bot"
)

func (n *Node) ListRemoteBots(nodeName string) ([]BotEntry, error) {
	target := n.findWatchdogNode(nodeName)
	if target == nil {
		return nil, fmt.Errorf("node %q not found", nodeName)
	}

	req := map[string]interface{}{
		"type":    TypeListBotsReq,
		"_secret": n.cfg.GlobalSecret,
	}

	var reply ListBotsReply
	if err := n.cluster.SendToWithResponse(target, MsgBotToWatchdog, req, &reply); err != nil {
		return nil, fmt.Errorf("list bots: %w", err)
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("list bots: %s", reply.Error)
	}
	return reply.Bots, nil
}

func (n *Node) ControlRemoteBot(nodeName, botID, action string) error {
	target := n.findWatchdogNode(nodeName)
	if target == nil {
		return fmt.Errorf("node %q not found", nodeName)
	}

	req := map[string]interface{}{
		"type":    TypeBotControlReq,
		"bot_id":  botID,
		"action":  action,
		"_secret": n.cfg.GlobalSecret,
	}

	var reply BotControlReply
	if err := n.cluster.SendToWithResponse(target, MsgBotToWatchdog, req, &reply); err != nil {
		return fmt.Errorf("control bot: %w", err)
	}
	if reply.Error != "" {
		return fmt.Errorf("control bot: %s", reply.Error)
	}
	return nil
}

func (n *Node) ControlRemoteBotAll(nodeName, action string) (int, error) {
	target := n.findWatchdogNode(nodeName)
	if target == nil {
		return 0, fmt.Errorf("node %q not found", nodeName)
	}

	req := map[string]interface{}{
		"type":    TypeBotControlReq,
		"action":  action,
		"_secret": n.cfg.GlobalSecret,
	}

	var reply BotControlReply
	if err := n.cluster.SendToWithResponse(target, MsgBotToWatchdog, req, &reply); err != nil {
		return 0, fmt.Errorf("control bot: %w", err)
	}
	if reply.Error != "" {
		return 0, fmt.Errorf("control bot: %s", reply.Error)
	}
	return reply.Count, nil
}

func (n *Node) FetchRemoteLogs(nodeName, botID string, lines int) (string, error) {
	target := n.findWatchdogNode(nodeName)
	if target == nil {
		return "", fmt.Errorf("node %q not found", nodeName)
	}

	if lines <= 0 {
		lines = 40
	}

	req := map[string]interface{}{
		"type":    TypeLogsReq,
		"bot_id":  botID,
		"lines":   lines,
		"_secret": n.cfg.GlobalSecret,
	}

	var reply LogsReply
	if err := n.cluster.SendToWithResponse(target, MsgBotToWatchdog, req, &reply); err != nil {
		return "", fmt.Errorf("fetch logs: %w", err)
	}
	if reply.Error != "" {
		return "", fmt.Errorf("fetch logs: %s", reply.Error)
	}
	return reply.Content, nil
}

func (n *Node) BotStats() (total, running int) {
	bots, err := n.manager.List()
	if err != nil {
		return 0, 0
	}
	total = len(bots)
	for _, b := range bots {
		if b.State.Status == bot.StatusRunning {
			running++
		}
	}
	return
}
