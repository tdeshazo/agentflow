package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

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

// providerOutputMu protects configured output sinks across all Provider values.
// Provider intentionally remains usable as a value, so the lock cannot live on
// an individual instance: value copies would not share it. A single lock also
// covers callers that configure stdout and stderr with the same writer.
var providerOutputMu sync.Mutex

type synchronizedWriter struct {
	writer io.Writer
}

func (w synchronizedWriter) Write(p []byte) (int, error) {
	providerOutputMu.Lock()
	defer providerOutputMu.Unlock()
	return w.writer.Write(p)
}

func synchronizeOutput(writer io.Writer) io.Writer {
	// Passing an *os.File directly lets the child inherit the descriptor, which
	// preserves terminal detection and native interactive output. Files are
	// already safe for concurrent use.
	if _, ok := writer.(*os.File); ok {
		return writer
	}
	return synchronizedWriter{writer: writer}
}

const defaultSandbox = "workspace-write"

const isolatedPermissionsProfile = "actor_isolated"

func (p Provider) Name() string { return "codex" }

// Contract declares the stable provider capabilities implemented by the Codex
// adapter. It intentionally omits modes such as human, remote-service, and
// nested-workflow that this adapter does not implement.
func (p Provider) Contract() provider.Contract {
	return provider.Contract{
		Version:                   provider.ContractVersionV2,
		Modes:                     []provider.ExecutionMode{provider.ExecutionModeAgent},
		InvocationContextVersions: []string{provider.InvocationContextVersionV1, provider.InvocationContextVersionV2},
		FilesystemBoundary:        true,
		ExecutionPolicy:           true,
		HandoffVersions:           []string{provider.HandoffVersionV1},
	}
}

func (p Provider) EnforcesFilesystemBoundary() bool { return true }

func (p Provider) EnforcesExecutionPolicy() bool { return true }

func (p Provider) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	bin := p.Binary
	if bin == "" {
		bin = "codex"
	}

	if err := validateRequest(req); err != nil {
		return provider.Result{}, err
	}

	var last string
	var temporary string
	if req.OutputLastMessage || req.Handoff != nil {
		tmp, err := os.MkdirTemp("", "agentflow-codex-*")
		if err != nil {
			return provider.Result{}, fmt.Errorf("create codex temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		temporary = tmp
		last = filepath.Join(tmp, "last-message.txt")
	}
	if req.Handoff != nil {
		if err := os.WriteFile(filepath.Join(temporary, "handoff-schema.json"), mustJSON(provider.HandoffJSONSchema()), 0o600); err != nil {
			return provider.Result{}, fmt.Errorf("write codex handoff schema: %w", err)
		}
		metadata := make(map[string]string, len(req.Metadata)+1)
		for key, value := range req.Metadata {
			metadata[key] = value
		}
		metadata["agentflow_handoff_schema"] = filepath.Join(temporary, "handoff-schema.json")
		req.Metadata = metadata
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
	secrets := credentialValues(req.Credentials)
	redactedStdout := newRedactingWriter(synchronizeOutput(stdout), secrets)
	redactedStderr := newRedactingWriter(synchronizeOutput(stderr), secrets)
	var meteredOutput bytes.Buffer
	cmd.Stdout = redactedStdout
	if req.Budget.Tokens > 0 {
		cmd.Stdout = io.MultiWriter(cmd.Stdout, &meteredOutput)
	}
	cmd.Stderr = redactedStderr
	cmd.Env = codexEnvironment(req)

	runErr := cmd.Run()
	closeErr := errors.Join(redactedStdout.Close(), redactedStderr.Close())
	if runErr != nil || closeErr != nil {
		return provider.Result{}, fmt.Errorf("codex exec: %w", errors.Join(runErr, closeErr))
	}
	usage, err := codexUsage(meteredOutput.Bytes())
	if err != nil {
		return provider.Result{}, err
	}
	if !req.OutputLastMessage && req.Handoff == nil {
		return provider.Result{Usage: usage}, nil
	}
	b, err := os.ReadFile(last)
	if err != nil {
		return provider.Result{}, fmt.Errorf("read codex final message: %w", err)
	}
	message := redactString(string(b), secrets)
	result := provider.Result{Usage: usage}
	if req.OutputLastMessage {
		result.FinalMessage = message
	}
	if req.Handoff != nil {
		if _, err := provider.ParseHandoff([]byte(message)); err != nil {
			return provider.Result{}, fmt.Errorf("validate codex structured handoff: %w", err)
		}
		result.Handoff = []byte(message)
	}
	return result, nil
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
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
			"-c", fmt.Sprintf("permissions.%s.network=%t", isolatedPermissionsProfile, req.Policy.Network == "allow"),
		)
	}
	if req.Budget.Tokens > 0 {
		args = append(args, "--json")
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
	if req.OutputLastMessage || req.Handoff != nil {
		args = append(args, "--output-last-message", lastMessage)
	}
	if req.Handoff != nil {
		// The schema file is created by Run and supplied through Metadata so this
		// pure argument builder remains directly testable.
		if schemaPath := req.Metadata["agentflow_handoff_schema"]; schemaPath != "" {
			args = append(args, "--output-schema", schemaPath)
		}
	}
	args = append(args, "-")
	return args
}

func validateRequest(req provider.Request) error {
	if req.Context.Version != provider.InvocationContextVersionV1 && req.Context.Version != provider.InvocationContextVersionV2 {
		return fmt.Errorf("codex provider does not support invocation context version %q", req.Context.Version)
	}
	if req.Handoff != nil && req.Handoff.Version != provider.HandoffVersionV1 {
		return fmt.Errorf("codex provider does not support handoff version %q", req.Handoff.Version)
	}
	if req.Approval != "" && req.Approval != "never" {
		return fmt.Errorf("codex provider supports approval policy \"never\" only, got %q", req.Approval)
	}
	if req.Policy.Network != "" && req.Policy.Network != "deny" && req.Policy.Network != "allow" {
		return fmt.Errorf("codex provider cannot enforce network policy %q", req.Policy.Network)
	}
	if len(req.Policy.Capabilities) != 0 {
		return fmt.Errorf("codex provider does not support external capabilities %q", req.Policy.Capabilities)
	}
	if req.Budget.CostUSD > 0 {
		return fmt.Errorf("codex provider cannot enforce a monetary budget")
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

func codexEnvironment(req provider.Request) []string {
	allowed := []string{"PATH", "HOME", "CODEX_HOME", "TMPDIR", "TERM", "COLORTERM", "NO_COLOR", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	environment := make([]string, 0, len(allowed)+len(req.Credentials))
	seen := make(map[string]bool, len(allowed)+len(req.Credentials))
	for _, name := range allowed {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
			seen[name] = true
		}
	}
	names := make([]string, 0, len(req.Credentials))
	for name := range req.Credentials {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !seen[name] {
			environment = append(environment, name+"="+req.Credentials[name])
		}
	}
	return environment
}

func codexUsage(output []byte) (provider.Usage, error) {
	if len(output) == 0 {
		return provider.Usage{}, nil
	}
	var usage provider.Usage
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(line, &event); err != nil {
			return provider.Usage{}, fmt.Errorf("decode codex metering event: %w", err)
		}
		collectCodexUsage(event, &usage)
	}
	return usage, nil
}

func collectCodexUsage(value any, usage *provider.Usage) {
	switch value := value.(type) {
	case map[string]any:
		if raw, ok := value["usage"].(map[string]any); ok {
			candidate := provider.Usage{
				InputTokens: int64(number(raw["input_tokens"])), OutputTokens: int64(number(raw["output_tokens"])),
				CostUSD: number(raw["cost_usd"]),
			}
			if candidate.InputTokens+candidate.OutputTokens > usage.InputTokens+usage.OutputTokens {
				*usage = candidate
			}
		}
		for _, child := range value {
			collectCodexUsage(child, usage)
		}
	case []any:
		for _, child := range value {
			collectCodexUsage(child, usage)
		}
	}
}

func number(value any) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return 0
}

type redactingWriter struct {
	mu      sync.Mutex
	writer  io.Writer
	secrets [][]byte
	pending []byte
	max     int
}

func newRedactingWriter(writer io.Writer, secrets [][]byte) *redactingWriter {
	redactor := &redactingWriter{writer: writer, secrets: secrets}
	for _, secret := range secrets {
		if len(secret) > redactor.max {
			redactor.max = len(secret)
		}
	}
	return redactor
}

func (w *redactingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	keep := w.max - 1
	if keep < 0 {
		keep = 0
	}
	limit := len(w.pending) - keep
	if limit <= 0 {
		return len(p), nil
	}
	consumed, err := w.flush(limit)
	w.pending = append(w.pending[:0], w.pending[consumed:]...)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *redactingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	consumed, err := w.flush(len(w.pending))
	w.pending = append(w.pending[:0], w.pending[consumed:]...)
	return err
}

func (w *redactingWriter) flush(limit int) (int, error) {
	index := 0
	var output bytes.Buffer
	for index < limit {
		matched := 0
		for _, secret := range w.secrets {
			if len(secret) > matched && len(w.pending)-index >= len(secret) && bytes.Equal(w.pending[index:index+len(secret)], secret) {
				matched = len(secret)
			}
		}
		if matched > 0 {
			output.WriteString("[REDACTED]")
			index += matched
			continue
		}
		output.WriteByte(w.pending[index])
		index++
	}
	written, err := w.writer.Write(output.Bytes())
	if err != nil {
		return 0, err
	}
	if written != output.Len() {
		return 0, io.ErrShortWrite
	}
	return index, nil
}

func credentialValues(credentials map[string]string) [][]byte {
	values := make([][]byte, 0, len(credentials))
	seen := map[string]bool{}
	for _, value := range credentials {
		if value != "" && !seen[value] {
			values = append(values, []byte(value))
			seen[value] = true
		}
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return values
}

func redactString(value string, secrets [][]byte) string {
	for _, secret := range secrets {
		value = strings.ReplaceAll(value, string(secret), "[REDACTED]")
	}
	return value
}

// RenderInvocationContext validates and renders the provider-neutral context
// deterministically. It resolves only the engine's stable workspace
// placeholder and does not reconstruct workflow semantics.
func RenderInvocationContext(context provider.InvocationContext, workspace string) (string, error) {
	if context.Version != provider.InvocationContextVersionV1 && context.Version != provider.InvocationContextVersionV2 {
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
	return "AgentFlow invocation context (" + context.Version + "):\n" + resolved + "\n", nil
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
