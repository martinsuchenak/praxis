package cluster

import (
	"os"
	"testing"

	logslog "github.com/paularlott/logger/slog"
)

func TestDefaultMulticastConfig(t *testing.T) {
	cfg := defaultMulticastConfig()
	if cfg.Group != "239.255.13.37" {
		t.Errorf("Group: got %q want %q", cfg.Group, "239.255.13.37")
	}
	if cfg.Port != 19373 {
		t.Errorf("Port: got %d want %d", cfg.Port, 19373)
	}
}

func TestHandleDiscoverMsg(t *testing.T) {
	tests := []struct {
		name     string
		msg      map[string]interface{}
		selfAddr string
		want     string
	}{
		{
			name:     "valid peer",
			msg:      map[string]interface{}{"type": "discover", "gossip_addr": "10.0.0.2:7700", "role": "watchdog"},
			selfAddr: "10.0.0.1:7700",
			want:     "10.0.0.2:7700",
		},
		{
			name:     "wrong type",
			msg:      map[string]interface{}{"type": "announce", "gossip_addr": "10.0.0.2:7700"},
			selfAddr: "10.0.0.1:7700",
			want:     "",
		},
		{
			name:     "self announcement",
			msg:      map[string]interface{}{"type": "discover", "gossip_addr": "10.0.0.1:7700"},
			selfAddr: "10.0.0.1:7700",
			want:     "",
		},
		{
			name:     "missing gossip_addr",
			msg:      map[string]interface{}{"type": "discover"},
			selfAddr: "10.0.0.1:7700",
			want:     "",
		},
		{
			name:     "empty gossip_addr",
			msg:      map[string]interface{}{"type": "discover", "gossip_addr": ""},
			selfAddr: "10.0.0.1:7700",
			want:     "",
		},
		{
			name:     "no type field",
			msg:      map[string]interface{}{"gossip_addr": "10.0.0.2:7700"},
			selfAddr: "10.0.0.1:7700",
			want:     "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := handleDiscoverMsg(tc.msg, tc.selfAddr)
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestMulticastInterface(t *testing.T) {
	intf, err := multicastInterface()
	if err != nil {
		t.Skipf("no multicast interface: %v", err)
	}
	if intf.Name == "" {
		t.Error("expected non-empty interface name")
	}
}

func TestStartDiscoveryDisabledOnEmptyConfig(t *testing.T) {
	log := logslog.New(logslog.Config{Level: "error"})
	ctx := t.Context()

	called := false
	joinFunc := func(addrs []string) error {
		called = true
		return nil
	}

	startDiscovery(ctx, multicastConfig{}, "1.2.3.4:7700", log, joinFunc)

	if called {
		t.Error("expected joinFunc not to be called with empty config")
	}
}

func TestEnvInt(t *testing.T) {
	tests := []struct {
		val  string
		def  int
		want int
	}{
		{"", 42, 42},
		{"100", 42, 100},
		{"abc", 42, 42},
		{"0", 42, 0},
	}
	for _, tc := range tests {
		t.Run(tc.val, func(t *testing.T) {
			t.Setenv("TEST_ENV_INT", tc.val)
			got := envInt("TEST_ENV_INT", tc.def)
			if got != tc.want {
				t.Errorf("envInt(%q, %d) = %d, want %d", tc.val, tc.def, got, tc.want)
			}
		})
	}
}

func TestConfigFromEnvMulticast(t *testing.T) {
	t.Setenv("BOT_MULTICAST_ADDR", "239.1.1.1")
	t.Setenv("BOT_MULTICAST_PORT", "9999")
	t.Setenv("BOT_WATCHDOG_PORT", "7700")
	t.Setenv("BOT_WATCHDOG_ADDR", "")

	cfg := ConfigFromEnv()
	if cfg.MulticastAddr != "239.1.1.1" {
		t.Errorf("MulticastAddr: got %q want %q", cfg.MulticastAddr, "239.1.1.1")
	}
	if cfg.MulticastPort != 9999 {
		t.Errorf("MulticastPort: got %d want %d", cfg.MulticastPort, 9999)
	}
}

func TestConfigFromEnvMulticastDefaults(t *testing.T) {
	os.Unsetenv("BOT_MULTICAST_ADDR")
	os.Unsetenv("BOT_MULTICAST_PORT")
	t.Setenv("BOT_WATCHDOG_PORT", "7700")
	t.Setenv("BOT_WATCHDOG_ADDR", "")

	cfg := ConfigFromEnv()
	if cfg.MulticastAddr != "" {
		t.Errorf("MulticastAddr should be empty when not set, got %q", cfg.MulticastAddr)
	}
	if cfg.MulticastPort != defaultMCPort {
		t.Errorf("MulticastPort: got %d want %d", cfg.MulticastPort, defaultMCPort)
	}
}

func TestDiscovererJoinCallback(t *testing.T) {
	log := logslog.New(logslog.Config{Level: "error"})
	d := &discoverer{
		cfg:     defaultMulticastConfig(),
		advAddr: "10.0.0.1:7700",
		log:     log,
	}

	msg := map[string]interface{}{
		"type":        "discover",
		"gossip_addr": "10.0.0.2:7700",
	}

	peerAddr := handleDiscoverMsg(msg, d.advAddr)
	if peerAddr != "10.0.0.2:7700" {
		t.Errorf("peerAddr: got %q want %q", peerAddr, "10.0.0.2:7700")
	}

	var joinedAddr string
	joinFunc := func(addrs []string) error {
		joinedAddr = addrs[0]
		return nil
	}

	d.mu.Lock()
	if !d.joined {
		d.joined = true
		if err := joinFunc([]string{peerAddr}); err != nil {
			t.Fatalf("joinFunc: %v", err)
		}
	}
	d.mu.Unlock()

	if joinedAddr != "10.0.0.2:7700" {
		t.Errorf("joinedAddr: got %q want %q", joinedAddr, "10.0.0.2:7700")
	}
}
