package hooks

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"praxis/internal/config"
)

func TestFireNoConfig(t *testing.T) {
	config.Set(nil)
	res, err := Fire("pre_spawn", "test-bot", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %v", res)
	}
}

func TestFireNoHandlers(t *testing.T) {
	config.Set(&config.Config{})
	res, err := Fire("pre_spawn", "test-bot", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %v", res)
	}
}

func TestFireCommandHook(t *testing.T) {
	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PreSpawn: []config.HookHandler{
				{
					Type:    "command",
					Command: "cat",
					Timeout: 5,
				},
			},
		},
	}
	config.Set(cfg)

	payload := map[string]interface{}{"goal": "test goal"}
	res, err := Fire("pre_spawn", "test-bot", payload)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}

	var evt Event
	input, _ := json.Marshal(evt)
	if len(input) == 0 {
		t.Log("event marshals ok")
	}
}

func TestFireCommandHookBlock(t *testing.T) {
	script := "#!/bin/bash\necho 'not allowed' >&2\nexit 2"
	tmpFile, err := os.CreateTemp("", "hook-block-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, _ = tmpFile.WriteString(script)
	_ = tmpFile.Close()
	_ = os.Chmod(tmpFile.Name(), 0o755)

	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PreSpawn: []config.HookHandler{
				{
					Type:    "command",
					Command: "bash " + tmpFile.Name(),
					Timeout: 5,
				},
			},
		},
	}
	config.Set(cfg)

	res, err := Fire("pre_spawn", "test-bot", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res == nil || !res.Block {
		t.Fatal("expected blocked result")
	}
	if res.Reason != "not allowed" {
		t.Fatalf("expected reason 'not allowed', got %q", res.Reason)
	}
}

func TestFireCommandHookContext(t *testing.T) {
	script := "#!/bin/bash\necho '{\"context\":\"extra info\"}'"
	tmpFile, err := os.CreateTemp("", "hook-ctx-*.sh")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()
	_, _ = tmpFile.WriteString(script)
	_ = tmpFile.Close()
	_ = os.Chmod(tmpFile.Name(), 0o755)

	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PreTick: []config.HookHandler{
				{
					Type:    "command",
					Command: tmpFile.Name(),
					Timeout: 5,
				},
			},
		},
	}
	config.Set(cfg)

	res, err := Fire("pre_tick", "test-bot", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Context != "extra info" {
		t.Fatalf("expected context 'extra info', got %q", res.Context)
	}
}

func TestFireHTTPHook(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var buf []byte
		buf, _ = json.Marshal(r.Body)
		_ = buf
		dec := json.NewDecoder(r.Body)
		var evt Event
		_ = dec.Decode(&evt)
		receivedBody, _ = json.Marshal(evt)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"context":"http-response"}`))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PostSpawn: []config.HookHandler{
				{
					Type:    "http",
					URL:     srv.URL,
					Timeout: 5,
				},
			},
		},
	}
	config.Set(cfg)

	res, err := Fire("post_spawn", "test-bot", map[string]interface{}{"goal": "hello"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.Context != "http-response" {
		t.Fatalf("expected context 'http-response', got %q", res.Context)
	}
	if len(receivedBody) == 0 {
		t.Fatal("expected server to receive body")
	}
}

func TestFireHTTPHookError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PostCrash: []config.HookHandler{
				{
					Type:    "http",
					URL:     srv.URL,
					Timeout: 5,
				},
			},
		},
	}
	config.Set(cfg)

	_, err := Fire("post_crash", "test-bot", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestFireAsyncHook(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(done)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{
		Hooks: config.HooksConfig{
			OnMessage: []config.HookHandler{
				{
					Type:    "http",
					URL:     srv.URL,
					Timeout: 5,
					Async:   true,
				},
			},
		},
	}
	config.Set(cfg)

	res, err := Fire("on_message", "test-bot", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if res != nil {
		t.Fatal("expected nil result for async hook")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("async hook did not fire")
	}
}

func TestHooksForEvent(t *testing.T) {
	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PreSpawn: []config.HookHandler{{Type: "command", Command: "echo"}},
			PostTick: []config.HookHandler{{Type: "http", URL: "http://example.com"}},
		},
	}

	preSpawn := cfg.HooksForEvent("pre_spawn")
	if len(preSpawn) != 1 {
		t.Fatalf("expected 1 pre_spawn handler, got %d", len(preSpawn))
	}
	postTick := cfg.HooksForEvent("post_tick")
	if len(postTick) != 1 {
		t.Fatalf("expected 1 post_tick handler, got %d", len(postTick))
	}
	unknown := cfg.HooksForEvent("unknown_event")
	if len(unknown) != 0 {
		t.Fatalf("expected 0 handlers for unknown event, got %d", len(unknown))
	}
}

func TestBotHooksAsDict(t *testing.T) {
	cfg := &config.Config{
		Hooks: config.HooksConfig{
			PreTick: []config.HookHandler{
				{Type: "command", Command: "echo hello"},
			},
			PostToolUse: []config.HookHandler{
				{Type: "http", URL: "http://example.com", Timeout: 10},
			},
		},
	}

	dict := cfg.BotHooksAsDict()
	if len(dict) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(dict))
	}

	preTick := dict[0].(map[string]interface{})
	if preTick["event"] != "pre_tick" {
		t.Fatalf("expected event 'pre_tick', got %v", preTick["event"])
	}
	if preTick["command"] != "echo hello" {
		t.Fatalf("expected command 'echo hello', got %v", preTick["command"])
	}

	postTool := dict[1].(map[string]interface{})
	if postTool["url"] != "http://example.com" {
		t.Fatalf("expected url, got %v", postTool["url"])
	}
	if postTool["timeout"] != 10 {
		t.Fatalf("expected timeout 10, got %v", postTool["timeout"])
	}
}
