package cluster

import "github.com/paularlott/gossip"

// MsgBotToWatchdog is the single gossip message type used for all bot→watchdog
// requests. This matches botcore.py's GOSSIP_MSG = gossip.MSG_USER (128).
// The payload contains a "type" field that selects the sub-handler.
const MsgBotToWatchdog = gossip.UserMsg // 128

// Sub-handler type strings (must match botcore.py values).
const (
	TypeShellReq = "shell_req"
	TypeSpawnReq = "spawn_req"
	TypeRelayReq = "relay_req"
)

// botRequest is the discriminator header — only "type" is decoded first.
type botRequest struct {
	Type string `msgpack:"type"`
}

// ShellRequest is sent by a bot to execute a shell command via the watchdog.
type ShellRequest struct {
	Type    string `msgpack:"type"`
	BotID   string `msgpack:"bot_id"`
	Command string `msgpack:"command"`
	CWD     string `msgpack:"cwd"`
	Timeout int    `msgpack:"timeout"` // seconds, 0 = default
	Secret  string `msgpack:"_secret"`
}

// ShellReply is sent back to the bot after the command completes.
type ShellReply struct {
	ExitCode int    `msgpack:"exit_code"`
	Stdout   string `msgpack:"stdout"`
	Stderr   string `msgpack:"stderr"`
	Error    string `msgpack:"error,omitempty"`
}

// SpawnRequest is sent by a bot to create a child bot.
type SpawnRequest struct {
	Type              string   `msgpack:"type"`
	Name              string   `msgpack:"name"`
	Goal              string   `msgpack:"goal"`
	Model             string   `msgpack:"model"`
	Brain             string   `msgpack:"brain,omitempty"`
	Thinking          bool     `msgpack:"thinking"`
	Workspace         string   `msgpack:"workspace,omitempty"`
	Scope             string   `msgpack:"scope,omitempty"`
	AllowedWorkspaces []string `msgpack:"allowed_workspaces,omitempty"`
	ParentID          string   `msgpack:"parent_id"`
	Secret            string   `msgpack:"_secret"`
}

// SpawnReply is sent back to the requesting bot.
type SpawnReply struct {
	BotID string `msgpack:"bot_id,omitempty"`
	Error string `msgpack:"error,omitempty"`
}

// RelayRequest is sent by a gateway bot to forward a message to another bot.
type RelayRequest struct {
	Type      string `msgpack:"type"`
	From      string `msgpack:"from"`
	TargetBot string `msgpack:"target_bot"`
	Content   string `msgpack:"content"`
	Secret    string `msgpack:"_secret"`
}

// RelayReply is sent back to the requesting bot.
type RelayReply struct {
	Status string `msgpack:"status,omitempty"`
	Error  string `msgpack:"error,omitempty"`
}
