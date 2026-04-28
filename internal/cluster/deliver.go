package cluster

import (
	"fmt"

	"praxis/internal/bot"
)

// deliverMessage sends a relayed message to a bot via gossip.
// Used by the relay handler to forward cross-workspace messages.
func (n *Node) deliverMessage(from string, target *bot.Bot, content string) error {
	if err := n.sendToBot(target.Config.Name, "relayed_message", map[string]interface{}{
		"from":    from,
		"content": content,
	}); err != nil {
		return fmt.Errorf("deliver to %s: %w", target.Config.Name, err)
	}
	return nil
}
