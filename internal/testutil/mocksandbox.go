package testutil

import (
	"context"
	"sync"

	"praxis/internal/sandbox"
)

// MockSandbox is a Sandbox implementation for tests that records calls and returns
// a fixed result.
type MockSandbox struct {
	mu      sync.Mutex
	calls   []sandbox.ExecOptions
	result  *sandbox.ExecResult
	execErr error
}

// NewMockSandbox returns a mock that succeeds with exit code 0 by default.
func NewMockSandbox() *MockSandbox {
	return &MockSandbox{result: &sandbox.ExecResult{ExitCode: 0}}
}

// SetResult configures the result returned by Execute.
func (m *MockSandbox) SetResult(r *sandbox.ExecResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.result = r
	m.execErr = err
}

func (m *MockSandbox) Execute(_ context.Context, opts sandbox.ExecOptions) (*sandbox.ExecResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, opts)
	if m.execErr != nil {
		return nil, m.execErr
	}
	return m.result, nil
}

func (m *MockSandbox) Available() bool { return true }
func (m *MockSandbox) Name() string    { return "mock" }

// Calls returns a copy of all recorded ExecOptions.
func (m *MockSandbox) Calls() []sandbox.ExecOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sandbox.ExecOptions, len(m.calls))
	copy(out, m.calls)
	return out
}

// CallCount returns the number of Execute calls made.
func (m *MockSandbox) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}
