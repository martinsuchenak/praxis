package cluster

import (
	"fmt"

	"github.com/paularlott/gossip"

	"praxis/internal/bot"
)

func (n *Node) handleSpawnReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req SpawnRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return spawnError("bad request: " + err.Error()), nil
	}

	if !n.validSecret(req.ParentID, req.Secret) {
		n.log.Warn("spawn_req: invalid secret", "parent", req.ParentID)
		return spawnError("invalid secret"), nil
	}

	parent, err := n.manager.Get(req.ParentID)
	if err != nil {
		return spawnError("unknown parent bot: " + req.ParentID), nil
	}

	// Scope inheritance: child scope must not exceed parent scope.
	if err := bot.ValidateChildScope(parent.Config.Scope, req.Scope); err != nil {
		return spawnError("scope violation: " + err.Error()), nil
	}

	// Workspace must be parent's own workspace or in parent's allowed_workspaces.
	if req.Workspace != "" {
		if !parentAllowsWorkspace(parent.Config, req.Workspace) {
			return spawnError(fmt.Sprintf("workspace %q not allowed by parent", req.Workspace)), nil
		}
	}

	// AllowedWorkspaces for the child must be a subset of parent's allowed_workspaces.
	for _, w := range req.AllowedWorkspaces {
		if !parentAllowsWorkspace(parent.Config, w) {
			return spawnError(fmt.Sprintf("allowed_workspace %q not permitted by parent", w)), nil
		}
	}

	// Inherit workspace path and secret from parent if child picks parent's workspace.
	var wsPath, wsSecret string
	if req.Workspace == parent.Config.Workspace {
		wsPath = parent.Config.WorkspacePath
		wsSecret = parent.Config.GossipSecret
	}

	childCfg := &bot.BotConfig{
		Name:              req.Name,
		Goal:              req.Goal,
		Model:             req.Model,
		Thinking:          req.Thinking,
		Brain:             req.Brain,
		Workspace:         req.Workspace,
		WorkspacePath:     wsPath,
		Scope:             req.Scope,
		AllowedWorkspaces: req.AllowedWorkspaces,
		Parent:            req.ParentID,
		GossipSecret:      wsSecret,
	}

	if err := n.manager.Create(childCfg); err != nil {
		return spawnError("create bot: " + err.Error()), nil
	}

	n.log.Info("spawned child bot", "parent", req.ParentID, "child", req.Name)
	return &SpawnReply{BotID: req.Name}, nil
}

// parentAllowsWorkspace returns true if the child may use workspace w.
func parentAllowsWorkspace(parentCfg *bot.BotConfig, w string) bool {
	if w == parentCfg.Workspace {
		return true
	}
	for _, allowed := range parentCfg.AllowedWorkspaces {
		if allowed == w {
			return true
		}
	}
	return false
}

func spawnError(msg string) *SpawnReply {
	return &SpawnReply{Error: msg}
}
