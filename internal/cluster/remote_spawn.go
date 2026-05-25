package cluster

import (
	"fmt"
	"strconv"

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
		if gn.AdvertisedAddr() == nodeName {
			return gn
		}
	}
	return nil
}

type WatchdogPeer struct {
	Name      string
	Addr      string
	BotsTotal int
	BotsRunning int
}

func (n *Node) WatchdogPeers() []WatchdogPeer {
	var peers []WatchdogPeer
	for _, gn := range n.cluster.AliveNodes() {
		if gn.Metadata.GetString("role") != "watchdog" {
			continue
		}
		name := gn.Metadata.GetString("node_name")
		if name == "" {
			continue
		}
		if name == n.cfg.NodeName {
			continue
		}
		total, _ := strconv.Atoi(gn.Metadata.GetString("bots_total"))
		running, _ := strconv.Atoi(gn.Metadata.GetString("bots_running"))
		peers = append(peers, WatchdogPeer{
			Name:       name,
			Addr:       gn.AdvertisedAddr(),
			BotsTotal:  total,
			BotsRunning: running,
		})
	}
	return peers
}

func (n *Node) LocalNodeName() string {
	return n.cfg.NodeName
}

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
		GossipSecret:      n.cfg.GlobalSecret,
		WatchdogNode:      n.cfg.NodeName,
	}

	if err := n.manager.Create(childCfg); err != nil {
		return spawnError("create bot: " + err.Error()), nil
	}

	n.log.Info("remote spawned bot", "name", req.Name)
	return &SpawnReply{BotID: req.Name}, nil
}
