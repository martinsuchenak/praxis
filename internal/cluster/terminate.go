package cluster

import (
	"github.com/paularlott/gossip"

	"praxis/internal/bot"
)

func (n *Node) handleTerminateReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req TerminateRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return &TerminateReply{Error: "bad request: " + err.Error()}, nil
	}

	if !n.validSecret(req.BotID, req.Secret) {
		n.log.Warn("terminate_req: invalid secret", "bot", req.BotID)
		return &TerminateReply{Error: "invalid secret"}, nil
	}

	if req.BotID == "" {
		return &TerminateReply{Error: "bot_id is required"}, nil
	}

	if err := n.manager.SetStatus(req.BotID, bot.StatusKilled); err != nil {
		return &TerminateReply{Error: "bot not found: " + req.BotID}, nil
	}

	n.log.Info("bot requested termination", "bot", req.BotID)
	return &TerminateReply{Status: "terminated"}, nil
}
