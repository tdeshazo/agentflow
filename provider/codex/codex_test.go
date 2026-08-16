package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/provider"
)

func TestBuildArgs(t *testing.T) {
	got := buildArgs(provider.Request{
		Workspace: "/repo", Model: "gpt-test", Reasoning: "high",
		Sandbox: "workspace-write", Ephemeral: true, Color: "never",
	}, "/tmp/last")
	want := []string{
		"exec", "--cd", "/repo", "-c", `approval_policy="never"`, "--sandbox", "workspace-write", "--ephemeral",
		"--color", "never", "--model", "gpt-test", "-c", `model_reasoning_effort="high"`,
		"--output-last-message", "/tmp/last", "-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRunPassesExplicitApprovalPolicy(t *testing.T) {
	workspace := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
printf '%s\n' "$@" > "$ARGS_FILE"
while [ "$#" -gt 0 ]; do
		case "$1" in
			--output-last-message) printf 'complete' > "$2"; shift 2 ;;
			*) shift ;;
		esac
done
`), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("ARGS_FILE", argsFile)

	p := Provider{Binary: fake}
	result, err := p.Run(context.Background(), provider.Request{
		Workspace: workspace,
		Model:     "gpt-test",
		Reasoning: "high",
		Prompt:    "perform the task",
		Sandbox:   "workspace-write",
		Approval:  "never",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinalMessage != "complete" {
		t.Fatalf("FinalMessage = %q, want complete", result.FinalMessage)
	}

	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
	for _, required := range []string{
		"-c", `approval_policy="never"`, "--sandbox", "workspace-write",
		"--model", "gpt-test", `model_reasoning_effort="high"`,
	} {
		if !contains(got, required) {
			t.Fatalf("Codex invocation missing %q: %#v", required, got)
		}
	}
	if contains(got, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("Codex invocation must not bypass sandboxing: %#v", got)
	}
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func TestRunRejectsUnsupportedApprovalPolicy(t *testing.T) {
	p := Provider{Binary: "does-not-matter"}
	_, err := p.Run(context.Background(), provider.Request{Workspace: t.TempDir(), Approval: "on-request"})
	if err == nil {
		t.Fatal("expected approval policy error")
	}
}
