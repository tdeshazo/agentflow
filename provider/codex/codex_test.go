package codex

import (
	"bytes"
	"context"
	"io"
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

func TestBuildArgsResolvesPresentationAtOutputBoundary(t *testing.T) {
	tests := []struct {
		name      string
		intent    provider.PresentationIntent
		outputTTY bool
		want      string
	}{
		{name: "attached auto", intent: provider.PresentationAuto, outputTTY: true, want: "auto"},
		{name: "attached always", intent: provider.PresentationAlways, outputTTY: true, want: "always"},
		{name: "attached never", intent: provider.PresentationNever, outputTTY: true, want: "never"},
		{name: "redirected auto", intent: provider.PresentationAuto, want: "never"},
		{name: "redirected always is plain", intent: provider.PresentationAlways, want: "never"},
		{name: "redirected never", intent: provider.PresentationNever, want: "never"},
		{name: "omitted attached defaults to auto", outputTTY: true, want: "auto"},
		{name: "omitted redirected defaults to never", want: "never"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildArgsForOutput(provider.Request{
				Workspace:    "/repo",
				Presentation: tt.intent,
			}, "/tmp/last", tt.outputTTY)
			for i := range args {
				if args[i] == "--color" {
					if i+1 >= len(args) || args[i+1] != tt.want {
						t.Fatalf("--color value = %#v, want %q", args[i+1:], tt.want)
					}
					return
				}
			}
			t.Fatal("Codex invocation did not include --color")
		})
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

func TestRunPreservesProviderStreamsWithoutAgentFlowANSI(t *testing.T) {
	workspace := t.TempDir()
	fake := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\n" +
		"printf 'provider stdout\\n'\n" +
		"printf 'provider diagnostic\\n' >&2\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"\tcase \"$1\" in\n" +
		"\t\t--output-last-message) printf 'complete' > \"$2\"; shift 2 ;;\n" +
		"\t\t*) shift ;;\n" +
		"\tesac\n" +
		"done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	var stdout, stderr bytes.Buffer
	p := Provider{
		Binary: fake,
		Stdout: &stdout,
		Stderr: &stderr,
		OutputTTY: func(io.Writer) bool {
			return false
		},
	}
	result, err := p.Run(context.Background(), provider.Request{
		Workspace:    workspace,
		Approval:     "never",
		Presentation: provider.PresentationAlways,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinalMessage != "complete" {
		t.Fatalf("FinalMessage = %q, want complete", result.FinalMessage)
	}
	if got, want := stdout.String(), "provider stdout\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "provider diagnostic\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	if strings.Contains(stdout.String()+stderr.String(), "\x1b[") {
		t.Fatal("AgentFlow inserted ANSI into provider streams")
	}
}

func TestRunAttachedTTYLeavesNativeProviderStylingUntouched(t *testing.T) {
	workspace := t.TempDir()
	fake := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\n" +
		"printf '\\033[31mprovider stdout\\033[0m\\n'\n" +
		"printf '\\033[33mprovider diagnostic\\033[0m\\n' >&2\n" +
		"while [ \"$#\" -gt 0 ]; do\n" +
		"\tcase \"$1\" in\n" +
		"\t\t--output-last-message) printf 'complete' > \"$2\"; shift 2 ;;\n" +
		"\t\t*) shift ;;\n" +
		"\tesac\n" +
		"done\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	var stdout, stderr bytes.Buffer
	p := Provider{
		Binary: fake,
		Stdout: &stdout,
		Stderr: &stderr,
		OutputTTY: func(io.Writer) bool {
			return true
		},
	}
	if _, err := p.Run(context.Background(), provider.Request{
		Workspace:    workspace,
		Approval:     "never",
		Presentation: provider.PresentationAuto,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "\x1b[31mprovider stdout\x1b[0m\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "\x1b[33mprovider diagnostic\x1b[0m\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
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
