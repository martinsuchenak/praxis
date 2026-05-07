package cluster

import (
	"fmt"

	"github.com/paularlott/gossip"

	"praxis/internal/bot"
)

// SpawnRemote sends a remote_spawn_req to the watchdog identified by nodeName,
// waits for the reply, and returns the bot ID of the newly created remote bot.
func (n *Node) SpawnRemote(nodeName string, cfg *bot.BotConfig) (string, error) {
	target := n.findWatchdogNode(nodeName)
	if target == nil {
		return "", fmt.Errorf("node %q not found in cluster", nodeName)
	}

	secret := n.cfg.GlobalSecret

	payload := map[string]interface{}{
		"type":     TypeRemoteSpawnReq,
		"name":     cfg.Name,
		"goal":     cfg.Goal,
		"model":    cfg.Model,
		"thinking": cfg.Thinking,
	}
	if cfg.Brain != "" {
		payload["brain"] = cfg.Brain
	}
	if cfg.Workspace != "" {
		payload["workspace"] = cfg.Workspace
	}
	if cfg.Scope != "" {
		payload["scope"] = cfg.Scope
	}
	if len(cfg.AllowedWorkspaces) > 0 {
		payload["allowed_workspaces"] = cfg.AllowedWorkspaces
	}
	if secret != "" {
		payload["_secret"] = secret
	}

	var reply SpawnReply
	if err := n.cluster.SendToWithResponse(target, MsgBotToWatchdog, payload, &reply); err != nil {
		return "", fmt.Errorf("remote spawn: %w", err)
	}
	if reply.Error != "" {
		return "", fmt.Errorf("remote spawn: %s", reply.Error)
	}
	return reply.BotID, nil
}

// findWatchdogNode returns the gossip node whose metadata has role=watchdog
// and node_name matching the given name.
func (n *Node) findWatchdogNode(nodeName string) *gossip.Node {
	for _, gn := range n.cluster.AliveNodes() {
		if gn.Metadata.GetString("role") != "watchdog" {
			continue
		}
		if gn.Metadata.GetString("node_name") == nodeName {
			return gn
		}
	}
	return nil
}

// ListWatchdogNodes returns the node names of all watchdog peers in the cluster
// (excluding self).
func (n *Node) ListWatchdogNodes() []string {
	var names []string
	for _, gn := range n.cluster.AliveNodes() {
		if gn.Metadata.GetString("role") == "watchdog" {
			if name := gn.Metadata.GetString("node_name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func (n *Node) handleRemoteSpawnReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req SpawnRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return spawnError("bad request: " + err.Error()), nil
	}

	if !n.validSecret("", req.Secret) {
		n.log.Warn("remote_spawn_req: invalid secret")
		return spawnError("invalid secret"), nil
	}

	if req.Name == "" || req.Goal == "" || req.Model == "" {
		return spawnError("name, goal, and model are required"), nil
	}

	childCfg := &bot.BotConfig{
		Name:              req.Name,
		Goal:              req.Goal,
		Model:             req.Model,
		Thinking:          req.Thinking,
		Brain:             req.Brain,
		Workspace:         req.Workspace,
		Scope:             req.Scope,
		AllowedWorkspaces: req.AllowedWorkspaces,
	}

	if err := n.manager.Create(childCfg); err != nil {
		return spawnError("create bot: " + err.Error()), nil
	}

	n.log.Info("remote spawned bot", "name", req.Name)
	return &SpawnReply{BotID: req.Name}, nil
}
