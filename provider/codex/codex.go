package codex

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/provider"
)

// Provider implements provider.Provider using the Codex CLI in non-interactive mode.
type Provider struct {
	Binary    string
	Stdout    io.Writer
	Stderr    io.Writer
	OutputTTY func(io.Writer) bool
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
	stdout := p.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := p.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	args := buildArgsForOutput(req, last, p.outputIsTTY(stdout) && p.outputIsTTY(stderr))
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.Workspace
	cmd.Stdin = bytes.NewBufferString(req.Prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

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
	return buildArgsForOutput(req, lastMessage, true)
}

func buildArgsForOutput(req provider.Request, lastMessage string, outputTTY bool) []string {
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
	args = append(args, "--color", colorPolicy(req.Presentation, outputTTY))
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Reasoning != "" {
		args = append(args, "-c", fmt.Sprintf("model_reasoning_effort=%q", req.Reasoning))
	}
	args = append(args, "--output-last-message", lastMessage, "-")
	return args
}

func (p Provider) outputIsTTY(out io.Writer) bool {
	if p.OutputTTY != nil {
		return p.OutputTTY(out)
	}
	return clioutput.IsTTY(out)
}

func colorPolicy(intent provider.PresentationIntent, outputTTY bool) string {
	switch intent {
	case provider.PresentationPlain:
		return "never"
	case provider.PresentationRich:
		if outputTTY {
			return "always"
		}
		return "never"
	case "", provider.PresentationAutomatic:
		if outputTTY {
			return "auto"
		}
		return "never"
	default:
		// Preserve the safe boundary for unknown or future intent values.
		if outputTTY {
			return "auto"
		}
		return "never"
	}
}
