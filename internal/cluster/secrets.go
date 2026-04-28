package cluster

// validSecret returns true if secret matches the bot's gossip_secret (from
// its workspace config) or the node-wide global secret.
// If no secret is configured anywhere and AuthDisabled is true, all requests
// are allowed. Otherwise, a missing secret configuration rejects requests.
func (n *Node) validSecret(botID, secret string) bool {
	b, err := n.manager.Get(botID)
	if err != nil {
		if n.cfg.GlobalSecret == "" {
			return n.cfg.AuthDisabled
		}
		return secret == n.cfg.GlobalSecret
	}

	botSecret := b.Config.GossipSecret

	if botSecret == "" && n.cfg.GlobalSecret == "" {
		return true
	}

	if botSecret != "" && secret == botSecret {
		return true
	}
	if n.cfg.GlobalSecret != "" && secret == n.cfg.GlobalSecret {
		return true
	}
	return false
}
