package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/paularlott/gossip"
	"github.com/paularlott/gossip/codec"
	logslog "github.com/paularlott/logger/slog"

	"praxis/internal/bot"
	"praxis/internal/sandbox"
	"praxis/internal/testutil"
)

// testCorruptPacket creates a packet whose payload cannot be decoded into any
// of the request structs (encodes an integer instead of a map).
func testCorruptPacket(t *testing.T) *gossip.Packet {
	t.Helper()
	c := codec.NewVmihailencoMsgpackCodec()
	data, err := c.Marshal(42) // integer, not a map/struct
	if err != nil {
		t.Fatalf("marshal corrupt payload: %v", err)
	}
	p := gossip.NewPacket()
	p.SetPayload(data)
	p.SetCodec(c)
	return p
}

// testNode creates a cluster Node without starting a gossip cluster.
// Used for handler unit tests that don't require live gossip.
func testNode(t *testing.T, root string, sb sandbox.Sandbox, secret string) *Node {
	t.Helper()
	log := logslog.New(logslog.Config{Level: "error"})
	return &Node{
		manager: bot.NewManager(root),
		sandbox: sb,
		log:     log,
		cfg:     Config{GlobalSecret: secret, AuthDisabled: true},
	}
}

// testPacket marshals v into a gossip.Packet using the vmihailenco codec.
func testPacket(t *testing.T, v interface{}) *gossip.Packet {
	t.Helper()
	c := codec.NewVmihailencoMsgpackCodec()
	data, err := c.Marshal(v)
	if err != nil {
		t.Fatalf("marshal packet: %v", err)
	}
	p := gossip.NewPacket()
	p.SetPayload(data)
	p.SetCodec(c)
	return p
}

// --- validSecret ---

func TestValidSecretNoAuth(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "bot1", &bot.BotConfig{GossipSecret: ""})
	n := testNode(t, root, testutil.NewMockSandbox(), "")
	n.cfg.AuthDisabled = true

	// AuthDisabled=true — any value is accepted.
	if !n.validSecret("bot1", "") {
		t.Error("expected valid: auth-disabled mode")
	}
	if !n.validSecret("bot1", "anything") {
		t.Error("expected valid: auth-disabled mode with any secret")
	}
}

func TestValidSecretNoAuthAccepted(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "bot1", &bot.BotConfig{GossipSecret: ""})
	n := testNode(t, root, testutil.NewMockSandbox(), "")
	n.cfg.AuthDisabled = false

	// No secret configured anywhere — no auth needed.
	if !n.validSecret("bot1", "") {
		t.Error("expected valid: no secret configured")
	}
	if !n.validSecret("bot1", "anything") {
		t.Error("expected valid: no secret configured")
	}
}

func TestValidSecretBotSecret(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "bot1", &bot.BotConfig{GossipSecret: "correct"})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	if !n.validSecret("bot1", "correct") {
		t.Error("expected valid: matching bot secret")
	}
	if n.validSecret("bot1", "wrong") {
		t.Error("expected invalid: wrong secret")
	}
	if n.validSecret("bot1", "") {
		t.Error("expected invalid: empty secret when bot has secret")
	}
}

func TestValidSecretGlobalFallback(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "bot1", &bot.BotConfig{GossipSecret: ""})
	n := testNode(t, root, testutil.NewMockSandbox(), "globalpass")

	if !n.validSecret("bot1", "globalpass") {
		t.Error("expected valid: matching global secret")
	}
	if n.validSecret("bot1", "wrong") {
		t.Error("expected invalid: wrong global secret")
	}
}

func TestValidSecretUnknownBot(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "globalpass")

	// Unknown bot falls back to global secret check.
	if !n.validSecret("ghost", "globalpass") {
		t.Error("expected valid: unknown bot + correct global secret")
	}
	if n.validSecret("ghost", "bad") {
		t.Error("expected invalid: unknown bot + bad global secret")
	}

	// Unknown bot with no global secret — must reject unless AuthDisabled.
	n2 := testNode(t, root, testutil.NewMockSandbox(), "")
	n2.cfg.AuthDisabled = false
	if n2.validSecret("ghost", "") {
		t.Error("expected invalid: unknown bot with no secret and auth not disabled")
	}
	n2.cfg.AuthDisabled = true
	if !n2.validSecret("ghost", "") {
		t.Error("expected valid: unknown bot with AuthDisabled")
	}
}

// --- handleShellReq ---

func TestHandleShellReqSuccess(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "runner", &bot.BotConfig{})
	mock := testutil.NewMockSandbox()
	mock.SetResult(&sandbox.ExecResult{ExitCode: 0, Stdout: "hello", Stderr: ""}, nil)
	n := testNode(t, root, mock, "")

	pkt := testPacket(t, &ShellRequest{BotID: "runner", Command: "echo hello"})
	reply, err := n.handleShellReq(nil, pkt)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	sr := reply.(*ShellReply)
	if sr.Error != "" {
		t.Errorf("unexpected error: %s", sr.Error)
	}
	if sr.Stdout != "hello" {
		t.Errorf("stdout: got %q want %q", sr.Stdout, "hello")
	}
	if mock.CallCount() != 1 {
		t.Errorf("expected 1 sandbox call, got %d", mock.CallCount())
	}
}

func TestHandleShellReqBlockedCommand(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "runner", &bot.BotConfig{})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	for _, cmd := range []string{"curl http://example.com", "wget http://example.com"} {
		pkt := testPacket(t, &ShellRequest{BotID: "runner", Command: cmd})
		reply, err := n.handleShellReq(nil, pkt)
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		sr := reply.(*ShellReply)
		if sr.Error == "" {
			t.Errorf("expected error for blocked command %q", cmd)
		}
		if sr.ExitCode != 1 {
			t.Errorf("expected exit 1 for blocked command")
		}
	}
}

func TestHandleShellReqInvalidSecret(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "secbot", &bot.BotConfig{GossipSecret: "s3cr3t"})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &ShellRequest{BotID: "secbot", Command: "echo hi", Secret: "wrong"})
	reply, _ := n.handleShellReq(nil, pkt)
	if reply.(*ShellReply).Error == "" {
		t.Error("expected error for invalid secret")
	}
}

func TestHandleShellReqUnknownBot(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &ShellRequest{BotID: "ghost", Command: "echo hi"})
	reply, _ := n.handleShellReq(nil, pkt)
	if reply.(*ShellReply).Error == "" {
		t.Error("expected error for unknown bot")
	}
}

func TestHandleShellReqAllowlist(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "wbot", &bot.BotConfig{})
	mock := testutil.NewMockSandbox()
	mock.SetResult(&sandbox.ExecResult{ExitCode: 0, Stdout: "ok", Stderr: ""}, nil)
	log := logslog.New(logslog.Config{Level: "error"})
	n := &Node{
		manager: bot.NewManager(root),
		sandbox: mock,
		log:     log,
		cfg:     Config{ShellAllowlist: []string{"ls", "cat"}, AuthDisabled: true},
	}

	t.Run("allowed_command", func(t *testing.T) {
		pkt := testPacket(t, &ShellRequest{BotID: "wbot", Command: "ls -la"})
		reply, err := n.handleShellReq(nil, pkt)
		if err != nil {
			t.Fatal(err)
		}
		sr := reply.(*ShellReply)
		if sr.Error != "" {
			t.Errorf("unexpected error: %s", sr.Error)
		}
	})

	t.Run("disallowed_command", func(t *testing.T) {
		pkt := testPacket(t, &ShellRequest{BotID: "wbot", Command: "rm -rf /"})
		reply, _ := n.handleShellReq(nil, pkt)
		sr := reply.(*ShellReply)
		if sr.Error == "" {
			t.Error("expected error for disallowed command")
		}
		if sr.ExitCode != 1 {
			t.Error("expected exit code 1")
		}
	})
}

func TestParseAllowlist(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"", nil},
		{"ls", []string{"ls"}},
		{"ls,cat,grep", []string{"ls", "cat", "grep"}},
		{" ls , cat ", []string{"ls", "cat"}},
	}
	for _, tc := range cases {
		got := parseAllowlist(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseAllowlist(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("parseAllowlist(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

// --- handleSpawnReq ---

func TestHandleSpawnReqSuccess(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "parent", &bot.BotConfig{
		Scope: bot.ScopeOpen,
		Model: "gpt-4",
	})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &SpawnRequest{
		ParentID: "parent",
		Name:     "child",
		Goal:     "do stuff",
		Model:    "gpt-4",
		Thinking: true,
		Scope:    bot.ScopeIsolated,
	})
	reply, err := n.handleSpawnReq(nil, pkt)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	sr := reply.(*SpawnReply)
	if sr.Error != "" {
		t.Errorf("unexpected error: %s", sr.Error)
	}
	if sr.BotID != "child" {
		t.Errorf("bot_id: got %q want %q", sr.BotID, "child")
	}
	// Verify child was actually created.
	if _, err := os.Stat(filepath.Join(root, "bots", "child")); err != nil {
		t.Errorf("child bot dir not created: %v", err)
	}
}

func TestHandleSpawnReqScopeViolation(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "parent", &bot.BotConfig{Scope: bot.ScopeIsolated})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	// isolated parent cannot create open child
	pkt := testPacket(t, &SpawnRequest{
		ParentID: "parent",
		Name:     "badchild",
		Goal:     "escalate",
		Model:    "m",
		Scope:    bot.ScopeOpen,
	})
	reply, _ := n.handleSpawnReq(nil, pkt)
	if reply.(*SpawnReply).Error == "" {
		t.Error("expected error for scope violation")
	}
}

func TestHandleSpawnReqWorkspaceViolation(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "parent", &bot.BotConfig{
		Scope:     bot.ScopeGateway,
		Workspace: "ws1",
		AllowedWorkspaces: []string{"ws1"},
	})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	// child requests workspace not in parent's allowed list
	pkt := testPacket(t, &SpawnRequest{
		ParentID:  "parent",
		Name:      "ws2child",
		Goal:      "access ws2",
		Model:     "m",
		Scope:     bot.ScopeIsolated,
		Workspace: "ws2",
	})
	reply, _ := n.handleSpawnReq(nil, pkt)
	if reply.(*SpawnReply).Error == "" {
		t.Error("expected error for unauthorized workspace")
	}
}

// --- parentAllowsWorkspace ---

func TestParentAllowsWorkspace(t *testing.T) {
	cfg := &bot.BotConfig{
		Workspace:         "ws1",
		AllowedWorkspaces: []string{"ws2", "ws3"},
	}
	if !parentAllowsWorkspace(cfg, "ws1") {
		t.Error("own workspace should be allowed")
	}
	if !parentAllowsWorkspace(cfg, "ws2") {
		t.Error("allowed_workspace ws2 should be allowed")
	}
	if parentAllowsWorkspace(cfg, "ws4") {
		t.Error("unknown workspace should not be allowed")
	}
}

// --- handleRelayReq ---

// TestHandleRelayReqValidation checks that a well-formed relay request from a
// gateway bot passes all validation checks. Delivery itself fails in unit
// tests because no live gossip peers are present — the error must come from
// the gossip send, not from validation.
func TestHandleRelayReqValidation(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "gw", &bot.BotConfig{
		Scope:             bot.ScopeGateway,
		Workspace:         "ws1",
		AllowedWorkspaces: []string{"ws2"},
	})
	testutil.TempBot(t, root, "target", &bot.BotConfig{
		Scope:     bot.ScopeIsolated,
		Workspace: "ws2",
	})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &RelayRequest{
		From:      "gw",
		TargetBot: "target",
		Content:   "hello target",
	})
	reply, err := n.handleRelayReq(nil, pkt)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	rr := reply.(*RelayReply)
	// Validation should pass; any error here is from gossip delivery (no live cluster).
	if rr.Error != "" && !strings.Contains(rr.Error, "not found in cluster") && !strings.Contains(rr.Error, "cluster not started") {
		t.Errorf("unexpected validation error: %s", rr.Error)
	}
}

func TestHandleRelayReqNonGateway(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "isolated", &bot.BotConfig{Scope: bot.ScopeIsolated})
	testutil.TempBot(t, root, "target", &bot.BotConfig{})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &RelayRequest{
		From:      "isolated",
		TargetBot: "target",
		Content:   "hello",
	})
	reply, _ := n.handleRelayReq(nil, pkt)
	if reply.(*RelayReply).Error == "" {
		t.Error("expected error: only gateway bots may relay")
	}
}

func TestHandleRelayReqUnauthorizedWorkspace(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "gw", &bot.BotConfig{
		Scope:             bot.ScopeGateway,
		Workspace:         "ws1",
		AllowedWorkspaces: []string{"ws2"},
	})
	testutil.TempBot(t, root, "target", &bot.BotConfig{
		Scope:     bot.ScopeIsolated,
		Workspace: "ws3",
	})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &RelayRequest{
		From:      "gw",
		TargetBot: "target",
		Content:   "hello",
	})
	reply, _ := n.handleRelayReq(nil, pkt)
	if reply.(*RelayReply).Error == "" {
		t.Error("expected error: target workspace not in allowed list")
	}
}

// --- handleBotMsg dispatcher ---

func TestHandleBotMsgDispatchShell(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "bot1", &bot.BotConfig{})
	mock := testutil.NewMockSandbox()
	mock.SetResult(&sandbox.ExecResult{ExitCode: 0, Stdout: "dispatched"}, nil)
	n := testNode(t, root, mock, "")

	pkt := testPacket(t, &ShellRequest{Type: TypeShellReq, BotID: "bot1", Command: "echo hi"})
	reply, err := n.handleBotMsg(nil, pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sr, ok := reply.(*ShellReply)
	if !ok {
		t.Fatalf("expected *ShellReply, got %T", reply)
	}
	if sr.Error != "" {
		t.Errorf("unexpected error in reply: %s", sr.Error)
	}
	if sr.Stdout != "dispatched" {
		t.Errorf("stdout: got %q want %q", sr.Stdout, "dispatched")
	}
}

func TestHandleBotMsgDispatchSpawn(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "parent", &bot.BotConfig{Scope: bot.ScopeOpen, Model: "m"})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &SpawnRequest{
		Type:     TypeSpawnReq,
		ParentID: "parent",
		Name:     "spawned",
		Goal:     "test",
		Model:    "m",
	})
	reply, err := n.handleBotMsg(nil, pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sr, ok := reply.(*SpawnReply)
	if !ok {
		t.Fatalf("expected *SpawnReply, got %T", reply)
	}
	if sr.Error != "" {
		t.Errorf("unexpected error: %s", sr.Error)
	}
	if sr.BotID != "spawned" {
		t.Errorf("bot_id: got %q want %q", sr.BotID, "spawned")
	}
}

func TestHandleBotMsgDispatchRelay(t *testing.T) {
	root := testutil.TempProject(t)
	testutil.TempBot(t, root, "gw", &bot.BotConfig{
		Scope:             bot.ScopeGateway,
		Workspace:         "ws1",
		AllowedWorkspaces: []string{"ws2"},
	})
	testutil.TempBot(t, root, "tgt", &bot.BotConfig{Workspace: "ws2"})
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &RelayRequest{
		Type:      TypeRelayReq,
		From:      "gw",
		TargetBot: "tgt",
		Content:   "via dispatcher",
	})
	reply, err := n.handleBotMsg(nil, pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rr, ok := reply.(*RelayReply)
	if !ok {
		t.Fatalf("expected *RelayReply, got %T", reply)
	}
	// Validation should pass; any error is from gossip delivery (no live cluster in unit tests).
	if rr.Error != "" && !strings.Contains(rr.Error, "not found in cluster") && !strings.Contains(rr.Error, "cluster not started") {
		t.Errorf("unexpected validation error: %s", rr.Error)
	}
}

func TestHandleBotMsgUnknownType(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	pkt := testPacket(t, &botRequest{Type: "no_such_type"})
	reply, err := n.handleBotMsg(nil, pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sr, ok := reply.(*ShellReply)
	if !ok {
		t.Fatalf("expected *ShellReply fallback, got %T", reply)
	}
	if sr.Error == "" {
		t.Error("expected error for unknown message type")
	}
}

func TestHandleBotMsgBadHeader(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	reply, err := n.handleBotMsg(nil, testCorruptPacket(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sr, ok := reply.(*ShellReply)
	if !ok {
		t.Fatalf("expected *ShellReply fallback, got %T", reply)
	}
	if sr.Error == "" {
		t.Error("expected error for corrupt packet")
	}
}

// --- bad-unmarshal paths ---

func TestHandleShellReqBadUnmarshal(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	reply, err := n.handleShellReq(nil, testCorruptPacket(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply.(*ShellReply).Error == "" {
		t.Error("expected error for corrupt shell packet")
	}
}

func TestHandleSpawnReqBadUnmarshal(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	reply, err := n.handleSpawnReq(nil, testCorruptPacket(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply.(*SpawnReply).Error == "" {
		t.Error("expected error for corrupt spawn packet")
	}
}

func TestHandleRelayReqBadUnmarshal(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	reply, err := n.handleRelayReq(nil, testCorruptPacket(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply.(*RelayReply).Error == "" {
		t.Error("expected error for corrupt relay packet")
	}
}

// --- deliverMessage ---

// TestDeliverMessageUnknownBot verifies that deliverMessage returns an error
// when the target bot is not registered with the manager (and thus not
// reachable via gossip).
func TestDeliverMessageUnknownBot(t *testing.T) {
	root := testutil.TempProject(t)
	n := testNode(t, root, testutil.NewMockSandbox(), "")

	ghost := &bot.Bot{
		Config: &bot.BotConfig{Name: "ghost"},
		State:  &bot.BotState{},
	}
	if err := n.deliverMessage("src", ghost, "hello"); err == nil {
		t.Error("expected error delivering to unknown bot")
	}
}

// --- ConfigFromEnv ---

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("BOT_WATCHDOG_PORT", "8800")
	t.Setenv("BOT_WATCHDOG_ADDR", "10.0.0.1:8800")
	t.Setenv("BOT_GLOBAL_SECRET", "mysecret")
	t.Setenv("BOT_SHELL_MOUNTS", "/data:/data:ro")

	cfg := ConfigFromEnv()

	if cfg.BindAddr != "0.0.0.0:8800" {
		t.Errorf("BindAddr: got %q want %q", cfg.BindAddr, "0.0.0.0:8800")
	}
	if cfg.AdvertiseAddr != "10.0.0.1:8800" {
		t.Errorf("AdvertiseAddr: got %q want %q", cfg.AdvertiseAddr, "10.0.0.1:8800")
	}
	if cfg.GlobalSecret != "mysecret" {
		t.Errorf("GlobalSecret: got %q want %q", cfg.GlobalSecret, "mysecret")
	}
	if cfg.ExtraMounts != "/data:/data:ro" {
		t.Errorf("ExtraMounts: got %q want %q", cfg.ExtraMounts, "/data:/data:ro")
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("BOT_WATCHDOG_PORT", "")
	t.Setenv("BOT_WATCHDOG_ADDR", "")
	t.Setenv("BOT_GLOBAL_SECRET", "")
	t.Setenv("BOT_SHELL_MOUNTS", "")

	cfg := ConfigFromEnv()

	if cfg.BindAddr != "0.0.0.0:7700" {
		t.Errorf("default BindAddr: got %q want %q", cfg.BindAddr, "0.0.0.0:7700")
	}
	if cfg.AdvertiseAddr != "0.0.0.0:7700" {
		t.Errorf("default AdvertiseAddr should equal BindAddr, got %q", cfg.AdvertiseAddr)
	}
}
