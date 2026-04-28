package cluster

import (
	"context"
	"strings"
	"time"

	"github.com/paularlott/gossip"

	"praxis/internal/sandbox"
)

// shellBlocked lists commands that bots are never allowed to run via the proxy.
var shellBlocked = map[string]bool{
	"curl": true,
	"wget": true,
}

func (n *Node) handleShellReq(_ *gossip.Node, pkt *gossip.Packet) (interface{}, error) {
	var req ShellRequest
	if err := pkt.Unmarshal(&req); err != nil {
		return shellError("bad request: " + err.Error()), nil
	}

	if !n.validSecret(req.BotID, req.Secret) {
		n.log.Warn("shell_req: invalid secret", "bot", req.BotID)
		return shellError("invalid secret"), nil
	}

	b, err := n.manager.Get(req.BotID)
	if err != nil {
		return shellError("unknown bot: " + req.BotID), nil
	}

	if fields := strings.Fields(req.Command); len(fields) > 0 {
		first := fields[0]
		if shellBlocked[first] {
			return shellError("command not allowed: " + first + ". Use http_request tool instead."), nil
		}
		if len(n.cfg.ShellAllowlist) > 0 {
			allowed := false
			for _, a := range n.cfg.ShellAllowlist {
				if a == first {
					allowed = true
					break
				}
			}
			if !allowed {
				return shellError(first + " is not in the shell allowlist (" + strings.Join(n.cfg.ShellAllowlist, ", ") + ")."), nil
			}
		}
	}

	var timeout time.Duration
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	result, err := n.sandbox.Execute(context.Background(), sandbox.ExecOptions{
		Command:       req.Command,
		BotDir:        b.Dir,
		CWD:           req.CWD,
		WorkspacePath: b.Config.WorkspacePath,
		Timeout:       timeout,
	})
	if err != nil {
		return shellError("sandbox error: " + err.Error()), nil
	}

	return &ShellReply{
		ExitCode: result.ExitCode,
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}, nil
}

func shellError(msg string) *ShellReply {
	return &ShellReply{Error: msg, ExitCode: 1}
}
