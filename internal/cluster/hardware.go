package cluster

import (
	"github.com/paularlott/gossip"
)

func (n *Node) handleHardwareReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req HardwareRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return &HardwareReply{Error: "bad request"}, nil
	}

	if !n.validSecret("", req.Secret) {
		return &HardwareReply{Error: "unauthorized"}, nil
	}

	if n.cluster == nil {
		return &HardwareReply{Error: "cluster not started"}, nil
	}

	var target *gossip.Node
	for _, gn := range n.cluster.AliveNodes() {
		if gn.Metadata.GetString("role") == "device" && gn.Metadata.GetString("id") == req.Node {
			if target == nil {
				target = gn
			}
			continue
		}
	}
	if target == nil {
		return &HardwareReply{Error: "device not found: " + req.Node}, nil
	}

	cmd := map[string]interface{}{
		"peripheral": req.Peripheral,
		"affordance": req.Affordance,
		"operation":  req.Operation,
	}
	if req.Input != nil {
		cmd["input"] = req.Input
	}

	var reply HardwareReply
	if err := n.cluster.SendToWithResponse(target, gossip.UserMsg, cmd, &reply); err != nil {
		return &HardwareReply{Error: "device communication failed: " + err.Error()}, nil
	}

	return &reply, nil
}
