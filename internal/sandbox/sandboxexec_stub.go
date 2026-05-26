//go:build !darwin

package sandbox

import "fmt"

func NewSandboxExecSandbox() (*SandboxExecSandbox, error) {
	return nil, fmt.Errorf("sandbox-exec is only available on macOS")
}

type SandboxExecSandbox struct{}

func (s *SandboxExecSandbox) Available() bool { return false }
func (s *SandboxExecSandbox) Name() string    { return "sandbox-exec" }
