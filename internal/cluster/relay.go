package cluster

import (
	"fmt"

	"github.com/paularlott/gossip"

	"praxis/internal/bot"
)

func (n *Node) handleRelayReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req RelayRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return relayError("bad request: " + err.Error()), nil
	}

	if !n.validSecret(req.From, req.Secret) {
		n.log.Warn("relay_req: invalid secret", "from", req.From)
		return relayError("invalid secret"), nil
	}

	src, err := n.manager.Get(req.From)
	if err != nil {
		return relayError("unknown source bot: " + req.From), nil
	}

	// Only gateway-scoped bots may relay messages.
	if src.Config.Scope != bot.ScopeGateway {
		return relayError(fmt.Sprintf("bot %q has scope %q; only gateway bots may relay", req.From, src.Config.Scope)), nil
	}

	// Target bot must exist.
	target, err := n.manager.Get(req.TargetBot)
	if err != nil {
		return relayError("unknown target bot: " + req.TargetBot), nil
	}

	// The target's workspace must be in the source's allowed_workspaces.
	if target.Config.Workspace != "" && !parentAllowsWorkspace(src.Config, target.Config.Workspace) {
		return relayError(fmt.Sprintf("target workspace %q not in source allowed_workspaces", target.Config.Workspace)), nil
	}

	// Write the message to the target bot's inbox.
	if err := n.deliverMessage(src.Config.Name, target, req.Content); err != nil {
		return relayError("deliver: " + err.Error()), nil
	}

	n.log.Info("relayed message", "from", req.From, "to", req.TargetBot)
	return &RelayReply{Status: "relayed"}, nil
}

func relayError(msg string) *RelayReply {
	return &RelayReply{Error: msg}
}
