//go:build !darwin

package sandbox

import (
	"context"
	"fmt"
)

type SandboxExecSandbox struct{}

func NewSandboxExecSandbox() (*SandboxExecSandbox, error) {
	return nil, fmt.Errorf("sandbox-exec is only available on macOS")
}

func (s *SandboxExecSandbox) Available() bool { return false }
func (s *SandboxExecSandbox) Name() string    { return "sandbox-exec" }
func (s *SandboxExecSandbox) Execute(_ context.Context, _ ExecOptions) (*ExecResult, error) {
	return nil, fmt.Errorf("sandbox-exec is only available on macOS")
}
