// Package local provides an explicit local-command provider adapter.
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"

	"github.com/tdeshazo/agentflow/provider"
)

// Provider runs one explicitly configured local command with the portable
// invocation context on stdin. It deliberately does not claim filesystem or
// execution-policy enforcement, so an Engine rejects it for isolated actor
// work rather than treating host-process execution as a sandbox.
type Provider struct {
	NameValue string
	Command   []string
}

func (p Provider) Name() string {
	if p.NameValue == "" {
		return "local-command"
	}
	return p.NameValue
}

func (p Provider) Contract() provider.Contract {
	return provider.Contract{
		Version:                   provider.ContractVersionV1,
		Modes:                     []provider.ExecutionMode{provider.ExecutionModeLocalCommand},
		InvocationContextVersions: []string{provider.InvocationContextVersion},
	}
}

func (p Provider) Run(ctx context.Context, request provider.Request) (provider.Result, error) {
	if len(p.Command) == 0 {
		return provider.Result{}, fmt.Errorf("local command provider has no command")
	}
	if request.Context.Version != provider.InvocationContextVersion {
		return provider.Result{}, fmt.Errorf("local command provider does not support invocation context version %q", request.Context.Version)
	}
	if len(request.FilesystemBoundary) != 0 || request.Policy.Network != "" || len(request.Policy.Capabilities) != 0 || request.Policy.ApprovalGate != "" || len(request.Credentials) != 0 {
		return provider.Result{}, fmt.Errorf("local command provider cannot enforce actor execution boundaries")
	}
	prompt, err := json.Marshal(request.Context)
	if err != nil {
		return provider.Result{}, fmt.Errorf("encode invocation context: %w", err)
	}
	command := exec.CommandContext(ctx, p.Command[0], p.Command[1:]...)
	command.Dir = request.Workspace
	var output bytes.Buffer
	command.Stdin = bytes.NewReader(prompt)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return provider.Result{}, fmt.Errorf("local command provider: %w", err)
	}
	return provider.Result{FinalMessage: output.String()}, nil
}
