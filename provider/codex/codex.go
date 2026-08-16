package codex

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tdeshazo/agentflow-spec/provider"
)

// Provider implements provider.Provider using the Codex CLI in non-interactive mode.
type Provider struct {
	Binary string
	Stdout io.Writer
	Stderr io.Writer
}

func (p Provider) Name() string { return "codex" }

func (p Provider) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	bin := p.Binary
	if bin == "" {
		bin = "codex"
	}

	tmp, err := os.MkdirTemp("", "agentflow-codex-*")
	if err != nil {
		return provider.Result{}, fmt.Errorf("create codex temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	if req.Approval != "" && req.Approval != "never" {
		return provider.Result{}, fmt.Errorf("codex provider supports approval policy \"never\" only, got %q", req.Approval)
	}

	last := filepath.Join(tmp, "last-message.txt")
	args := buildArgs(req, last)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.Workspace
	cmd.Stdin = bytes.NewBufferString(req.Prompt)
	if p.Stdout != nil {
		cmd.Stdout = p.Stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if p.Stderr != nil {
		cmd.Stderr = p.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return provider.Result{}, fmt.Errorf("codex exec: %w", err)
	}
	b, err := os.ReadFile(last)
	if err != nil {
		return provider.Result{}, fmt.Errorf("read codex final message: %w", err)
	}
	return provider.Result{FinalMessage: string(b)}, nil
}

func buildArgs(req provider.Request, lastMessage string) []string {
	args := []string{"exec", "--cd", req.Workspace}
	// Codex loads user configuration by default. Override its approval setting so
	// the workflow's only supported policy remains authoritative for this run.
	args = append(args, "-c", `approval_policy="never"`)
	if req.Sandbox != "" {
		args = append(args, "--sandbox", req.Sandbox)
	}
	if req.Ephemeral {
		args = append(args, "--ephemeral")
	}
	if req.Color != "" {
		args = append(args, "--color", req.Color)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Reasoning != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", req.Reasoning))
	}
	args = append(args, "--output-last-message", lastMessage, "-")
	return args
}
