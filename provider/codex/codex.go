package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

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

const defaultSandbox = "workspace-write"

const isolatedPermissionsProfile = "actor_isolated"

func (p Provider) Name() string { return "codex" }

func (p Provider) EnforcesFilesystemBoundary() bool { return true }

func (p Provider) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	bin := p.Binary
	if bin == "" {
		bin = "codex"
	}

	if err := validateRequest(req); err != nil {
		return provider.Result{}, err
	}

	var last string
	if req.OutputLastMessage {
		tmp, err := os.MkdirTemp("", "agentflow-codex-*")
		if err != nil {
			return provider.Result{}, fmt.Errorf("create codex temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		last = filepath.Join(tmp, "last-message.txt")
	}
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
	prompt, err := RenderInvocationContext(req.Context, req.Workspace)
	if err != nil {
		return provider.Result{}, err
	}
	cmd.Stdin = bytes.NewBufferString(prompt)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return provider.Result{}, fmt.Errorf("codex exec: %w", err)
	}
	if !req.OutputLastMessage {
		return provider.Result{}, nil
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
	if len(req.FilesystemBoundary) == 0 {
		// Keep the provider-neutral request unchanged at the engine boundary, but
		// make the built-in adapter's empty-sandbox behavior explicit to Codex.
		args = append(args, "--sandbox", resolveSandbox(req.Sandbox))
	} else {
		args = append(
			args,
			"--ignore-user-config",
			"--strict-config",
			"-c", fmt.Sprintf("default_permissions=%q", isolatedPermissionsProfile),
			"-c", fmt.Sprintf("permissions.%s.extends=%q", isolatedPermissionsProfile, codexPermissionsBase(req.Sandbox)),
			"-c", fmt.Sprintf("permissions.%s.filesystem=%s", isolatedPermissionsProfile, codexFilesystemBoundary(req.FilesystemBoundary)),
		)
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
	if req.OutputLastMessage {
		args = append(args, "--output-last-message", lastMessage)
	}
	args = append(args, "-")
	return args
}

func validateRequest(req provider.Request) error {
	if req.Context.Version != provider.InvocationContextVersion {
		return fmt.Errorf("codex provider does not support invocation context version %q", req.Context.Version)
	}
	if req.Approval != "" && req.Approval != "never" {
		return fmt.Errorf("codex provider supports approval policy \"never\" only, got %q", req.Approval)
	}
	if len(req.FilesystemBoundary) == 0 {
		return nil
	}
	sandbox := resolveSandbox(req.Sandbox)
	if sandbox == "danger-full-access" {
		return fmt.Errorf("codex provider cannot enforce the actor read boundary with sandbox %q", sandbox)
	}
	if sandbox != "workspace-write" && sandbox != "read-only" {
		return fmt.Errorf("codex provider cannot enforce the actor read boundary with unsupported sandbox %q", sandbox)
	}
	seen := make(map[string]provider.FilesystemAccess, len(req.FilesystemBoundary))
	for _, rule := range req.FilesystemBoundary {
		if rule.Path == "" || !filepath.IsAbs(rule.Path) || filepath.Clean(rule.Path) != rule.Path {
			return fmt.Errorf("codex provider actor read boundary has invalid path %q", rule.Path)
		}
		if rule.Access != provider.FilesystemRead && rule.Access != provider.FilesystemDeny {
			return fmt.Errorf("codex provider actor read boundary has invalid access %q for %q", rule.Access, rule.Path)
		}
		if prior, ok := seen[rule.Path]; ok && prior != rule.Access {
			return fmt.Errorf("codex provider actor read boundary has conflicting access for %q", rule.Path)
		}
		seen[rule.Path] = rule.Access
	}
	return nil
}

// RenderInvocationContext validates and renders the provider-neutral context
// deterministically. It resolves only the engine's stable workspace
// placeholder and does not reconstruct workflow semantics.
func RenderInvocationContext(context provider.InvocationContext, workspace string) (string, error) {
	if context.Version != provider.InvocationContextVersion {
		return "", fmt.Errorf("codex provider does not support invocation context version %q", context.Version)
	}
	if workspace == "" || !filepath.IsAbs(workspace) {
		return "", fmt.Errorf("codex provider requires an absolute workspace to render invocation context")
	}
	b, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return "", fmt.Errorf("render codex invocation context: %w", err)
	}
	encodedWorkspace, err := json.Marshal(filepath.Clean(workspace))
	if err != nil {
		return "", fmt.Errorf("render codex workspace: %w", err)
	}
	resolved := strings.ReplaceAll(string(b), provider.WorkspacePlaceholder, string(encodedWorkspace[1:len(encodedWorkspace)-1]))
	return "AgentFlow invocation context (" + provider.InvocationContextVersion + "):\n" + resolved + "\n", nil
}

func codexPermissionsBase(sandbox string) string {
	if resolveSandbox(sandbox) == "read-only" {
		return ":read-only"
	}
	return ":workspace"
}

func codexFilesystemBoundary(rules []provider.FilesystemRule) string {
	byPath := make(map[string]provider.FilesystemAccess, len(rules))
	paths := make([]string, 0, len(rules))
	for _, rule := range rules {
		if _, ok := byPath[rule.Path]; !ok {
			paths = append(paths, rule.Path)
		}
		byPath[rule.Path] = rule.Access
	}
	sort.Strings(paths)
	entries := make([]string, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, strconv.Quote(path)+"="+strconv.Quote(string(byPath[path])))
	}
	return "{" + strings.Join(entries, ",") + "}"
}

func resolveSandbox(sandbox string) string {
	if sandbox == "" {
		return defaultSandbox
	}
	return sandbox
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
