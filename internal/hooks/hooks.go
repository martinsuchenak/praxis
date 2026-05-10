package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"praxis/internal/config"
)

type Event struct {
	Name    string                 `json:"hook_event_name"`
	BotID   string                 `json:"bot_id,omitempty"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

type Result struct {
	Block   bool   `json:"block,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Context string `json:"context,omitempty"`
}

func Fire(eventName string, botID string, payload map[string]interface{}) (*Result, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, nil
	}
	handlers := cfg.HooksForEvent(eventName)
	if len(handlers) == 0 {
		return nil, nil
	}

	evt := Event{
		Name:    eventName,
		BotID:   botID,
		Payload: payload,
	}

	var lastResult *Result
	for _, h := range handlers {
		timeout := time.Duration(h.Timeout) * time.Second
		if timeout == 0 {
			timeout = 30 * time.Second
		}

		if h.Async {
			go func(hh config.HookHandler) {
				_, _ = runHandler(hh, evt, timeout)
			}(h)
			continue
		}

		res, err := runHandler(h, evt, timeout)
		if err != nil {
			return nil, fmt.Errorf("hook %s (%s): %w", eventName, h.Type, err)
		}
		if res != nil && res.Block {
			return res, nil
		}
		lastResult = res
	}
	return lastResult, nil
}

func runHandler(h config.HookHandler, evt Event, timeout time.Duration) (*Result, error) {
	switch h.Type {
	case "command":
		return runCommand(h.Command, evt, timeout)
	case "http":
		return runHTTP(h.URL, h.Headers, evt, timeout)
	default:
		return nil, fmt.Errorf("unknown hook type: %s", h.Type)
	}
}

func runCommand(command string, evt Event, timeout time.Duration) (*Result, error) {
	input, err := json.Marshal(evt)
	if err != nil {
		return nil, err
	}

	ctx := exec.Command("bash", "-c", command)
	ctx.Stdin = bytes.NewReader(input)
	ctx.Env = append(os.Environ(),
		"PRAXIS_HOOK_EVENT="+evt.Name,
		"PRAXIS_BOT_ID="+evt.BotID,
	)

	var stdout, stderr bytes.Buffer
	ctx.Stdout = &stdout
	ctx.Stderr = &stderr

	timer := time.AfterFunc(timeout, func() {
		_ = ctx.Process.Kill()
	})
	defer timer.Stop()

	err = ctx.Run()
	if err != nil {
		if ctx.ProcessState != nil {
			exitCode := ctx.ProcessState.ExitCode()
			if exitCode == 2 {
				return &Result{
					Block:  true,
					Reason: strings.TrimSpace(stderr.String()),
				}, nil
			}
		}
		return nil, fmt.Errorf("exit %d: %s", ctx.ProcessState.ExitCode(), stderr.String())
	}

	out := stdout.Bytes()
	if len(out) == 0 {
		return nil, nil
	}

	var res Result
	if err := json.Unmarshal(out, &res); err != nil {
		return &Result{Context: strings.TrimSpace(string(out))}, nil
	}
	return &res, nil
}

func runHTTP(url string, headers map[string]string, evt Event, timeout time.Duration) (*Result, error) {
	body, err := json.Marshal(evt)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if len(respBody) == 0 {
		return nil, nil
	}

	var res Result
	if err := json.Unmarshal(respBody, &res); err != nil {
		return &Result{Context: strings.TrimSpace(string(respBody))}, nil
	}
	return &res, nil
}
