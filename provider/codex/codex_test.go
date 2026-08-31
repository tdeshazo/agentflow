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
	"sync"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestBuildArgs(t *testing.T) {
	got := buildArgs(provider.Request{
		Workspace: "/repo", Model: "gpt-test", Reasoning: "high",
		Sandbox: "workspace-write", Ephemeral: true, OutputLastMessage: true, Presentation: provider.PresentationNever,
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

func TestBuildArgsResolvesSandbox(t *testing.T) {
	tests := []struct {
		name    string
		sandbox string
		want    string
	}{
		{name: "empty defaults to workspace-write", want: "workspace-write"},
		{name: "explicit workspace-write", sandbox: "workspace-write", want: "workspace-write"},
		{name: "explicit read-only", sandbox: "read-only", want: "read-only"},
		{name: "explicit danger-full-access", sandbox: "danger-full-access", want: "danger-full-access"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildArgs(provider.Request{Workspace: "/repo", Sandbox: tt.sandbox}, "/tmp/last")
			if got := sandboxArg(args); got != tt.want {
				t.Fatalf("--sandbox value = %q, want %q: %#v", got, tt.want, args)
			}
		})
	}
}

func TestBuildArgsEmptySandboxCannotInheritCodexUserConfiguration(t *testing.T) {
	args := buildArgs(provider.Request{Workspace: "/repo"}, "/tmp/last")
	if got := sandboxArg(args); got != defaultSandbox {
		t.Fatalf("empty sandbox argument = %q, want explicit %q: %#v", got, defaultSandbox, args)
	}
}

func TestBuildArgsEnforcesActorFilesystemBoundary(t *testing.T) {
	args := buildArgs(provider.Request{
		Workspace: "/quarantine",
		Sandbox:   "workspace-write",
		FilesystemBoundary: []provider.FilesystemRule{
			{Path: "/authoritative", Access: provider.FilesystemDeny},
		},
	}, "/tmp/last")
	for _, forbidden := range []string{"--sandbox", "danger-full-access", ".agentflow", "agentflow-spec"} {
		if contains(args, forbidden) || strings.Contains(strings.Join(args, "\n"), forbidden) {
			t.Fatalf("isolated Codex args expose forbidden value %q: %#v", forbidden, args)
		}
	}
	for _, required := range []string{
		"--ignore-user-config",
		"--strict-config",
		`default_permissions="actor_isolated"`,
		`permissions.actor_isolated.extends=":workspace"`,
		`permissions.actor_isolated.filesystem={"/authoritative"="deny"}`,
	} {
		if !contains(args, required) {
			t.Fatalf("isolated Codex args missing %q: %#v", required, args)
		}
	}
}

func TestValidateRequestRejectsUnenforceableActorFilesystemBoundary(t *testing.T) {
	err := validateRequest(provider.Request{
		Context: provider.InvocationContext{Version: provider.InvocationContextVersion},
		Sandbox: "danger-full-access",
		FilesystemBoundary: []provider.FilesystemRule{{
			Path:   "/authoritative",
			Access: provider.FilesystemDeny,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot enforce the actor read boundary") {
		t.Fatalf("validateRequest() error = %v, want fail-closed sandbox rejection", err)
	}
}

func TestNormalizedAgentsUseTheSameCodexSandboxBehavior(t *testing.T) {
	tests := []struct {
		name     string
		document string
	}{
		{
			name: "v1alpha1",
			document: `
apiVersion: agentflow.dev/v1alpha1
kind: AgentWorkflow
metadata: {name: v1alpha1}
spec:
  agents: {worker: {runner: codex}}
`,
		},
		{
			name: "v1alpha2",
			document: `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: v1alpha2}
spec:
  agents: {worker: {runner: codex, model: test-model}}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := writeWorkflow(t, tt.document)
			decoded, err := workflow.Decode(document)
			if err != nil {
				t.Fatal(err)
			}
			normalized, err := workflow.NormalizeWorkflow(decoded)
			if err != nil {
				t.Fatal(err)
			}
			agent := normalized.Workflow.Spec.Agents["worker"]
			if agent.Sandbox != "" {
				t.Fatalf("normalized sandbox = %q, want provider-neutral empty value", agent.Sandbox)
			}
			if got := sandboxArg(buildArgs(provider.Request{Workspace: "/repo", Sandbox: agent.Sandbox}, "/tmp/last")); got != defaultSandbox {
				t.Fatalf("Codex sandbox argument = %q, want %q", got, defaultSandbox)
			}
		})
	}
}

func TestBuildArgsOutputLastMessage(t *testing.T) {
	for _, test := range []struct {
		name    string
		capture bool
		want    bool
	}{
		{name: "capture enabled", capture: true, want: true},
		{name: "capture disabled", capture: false, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := buildArgs(provider.Request{Workspace: "/repo", OutputLastMessage: test.capture}, "/tmp/last")
			if got := contains(args, "--output-last-message"); got != test.want {
				t.Fatalf("--output-last-message present = %t, want %t: %#v", got, test.want, args)
			}
		})
	}
}

func TestBuildArgsResolvesPresentationAtOutputBoundary(t *testing.T) {
	tests := []struct {
		name      string
		intent    provider.PresentationIntent
		outputTTY bool
		want      string
	}{
		{name: "attached automatic", intent: provider.PresentationAutomatic, outputTTY: true, want: "auto"},
		{name: "attached rich", intent: provider.PresentationRich, outputTTY: true, want: "always"},
		{name: "attached plain", intent: provider.PresentationPlain, outputTTY: true, want: "never"},
		{name: "redirected automatic", intent: provider.PresentationAutomatic, want: "never"},
		{name: "redirected rich is plain", intent: provider.PresentationRich, want: "never"},
		{name: "redirected plain", intent: provider.PresentationPlain, want: "never"},
		{name: "omitted attached defaults to automatic", outputTTY: true, want: "auto"},
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

	p := Provider{
		Binary: fake,
		Stdout: io.Discard,
		Stderr: io.Discard,
		OutputTTY: func(io.Writer) bool {
			return false
		},
	}
	result, err := p.Run(context.Background(), provider.Request{
		Workspace:         workspace,
		Model:             "gpt-test",
		Reasoning:         "high",
		Context:           testInvocationContext("perform the task"),
		Sandbox:           "workspace-write",
		Approval:          "never",
		OutputLastMessage: true,
		Presentation:      provider.PresentationAlways,
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
		"--output-last-message",
	} {
		if !contains(got, required) {
			t.Fatalf("Codex invocation missing %q: %#v", required, got)
		}
	}
	if contains(got, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("Codex invocation must not bypass sandboxing: %#v", got)
	}
	for i := range got {
		if got[i] == "--color" {
			if i+1 >= len(got) || got[i+1] != "never" {
				t.Fatalf("redirected Codex color = %#v, want never", got[i:])
			}
			return
		}
	}
	t.Fatal("redirected Codex invocation omitted --color")
}

func TestRunPreservesProviderStreamsWithoutAgentFlowANSI(t *testing.T) {
	workspace := t.TempDir()
	fake := filepath.Join(t.TempDir(), "codex")
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$ARGS_FILE\"\n" +
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
	t.Setenv("ARGS_FILE", argsFile)

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
		Workspace:         workspace,
		Context:           testInvocationContext("perform the task"),
		Approval:          "never",
		OutputLastMessage: true,
		Presentation:      provider.PresentationAlways,
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
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read redirected args: %v", err)
	}
	if got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"); !containsColorArg(got, "never") {
		t.Fatalf("redirected Codex args = %#v, want --color never", got)
	}
}

func TestRunAttachedTTYLeavesNativeProviderStylingUntouched(t *testing.T) {
	workspace := t.TempDir()
	fake := filepath.Join(t.TempDir(), "codex")
	argsFile := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$ARGS_FILE\"\n" +
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
	t.Setenv("ARGS_FILE", argsFile)

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
		Workspace:         workspace,
		Context:           testInvocationContext("perform the task"),
		Approval:          "never",
		OutputLastMessage: true,
		Presentation:      provider.PresentationAuto,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got, want := stdout.String(), "\x1b[31mprovider stdout\x1b[0m\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "\x1b[33mprovider diagnostic\x1b[0m\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read attached args: %v", err)
	}
	if got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"); !containsColorArg(got, "auto") {
		t.Fatalf("attached Codex args = %#v, want --color auto", got)
	}
}

func TestRunSerializesConcurrentWritesToSharedStreams(t *testing.T) {
	workspace := t.TempDir()
	fake := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
i=0
while [ "$i" -lt 200 ]; do
	printf 'provider stdout\n'
	printf 'provider stderr\n' >&2
	i=$((i + 1))
done
`), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	var output bytes.Buffer
	p := Provider{
		Binary: fake,
		Stdout: &output,
		Stderr: &output,
		OutputTTY: func(io.Writer) bool {
			return false
		},
	}

	const runs = 8
	start := make(chan struct{})
	errs := make(chan error, runs)
	var group sync.WaitGroup
	group.Add(runs)
	for range runs {
		go func() {
			defer group.Done()
			<-start
			_, err := p.Run(context.Background(), provider.Request{
				Workspace: workspace,
				Context:   testInvocationContext("perform the task"),
				Approval:  "never",
			})
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}

	if got, want := strings.Count(output.String(), "provider stdout\n"), runs*200; got != want {
		t.Fatalf("stdout line count = %d, want %d", got, want)
	}
	if got, want := strings.Count(output.String(), "provider stderr\n"), runs*200; got != want {
		t.Fatalf("stderr line count = %d, want %d", got, want)
	}
}

func TestRunWithoutOutputLastMessageDoesNotReadCaptureFile(t *testing.T) {
	workspace := t.TempDir()
	argsFile := filepath.Join(t.TempDir(), "args")
	fake := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(fake, []byte(`#!/bin/sh
printf '%s\n' "$@" > "$ARGS_FILE"
`), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	t.Setenv("ARGS_FILE", argsFile)

	p := Provider{Binary: fake, Stdout: io.Discard, Stderr: io.Discard}
	result, err := p.Run(context.Background(), provider.Request{Workspace: workspace, Approval: "never", Context: testInvocationContext("perform the task")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.FinalMessage != "" {
		t.Fatalf("FinalMessage = %q, want empty", result.FinalMessage)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read captured args: %v", err)
	}
	if got := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"); contains(got, "--output-last-message") {
		t.Fatalf("Codex invocation unexpectedly captured final message: %#v", got)
	}
}

func containsColorArg(args []string, want string) bool {
	for i := range args {
		if args[i] == "--color" && i+1 < len(args) && args[i+1] == want {
			return true
		}
	}
	return false
}

func sandboxArg(args []string) string {
	for i := range args {
		if args[i] == "--sandbox" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func writeWorkflow(t *testing.T, document string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workflow.yaml")
	if err := os.WriteFile(path, []byte(document), 0o644); err != nil {
		t.Fatalf("write workflow: %v", err)
	}
	return path
}

func contains(values []string, want string) bool {
	return slices.Contains(values, want)
}

func testInvocationContext(objective string) provider.InvocationContext {
	return provider.InvocationContext{
		Version:   provider.InvocationContextVersion,
		Objective: objective,
		Workspace: provider.WorkspaceContext{Root: provider.WorkspacePlaceholder},
	}
}

func TestRunRejectsUnsupportedApprovalPolicy(t *testing.T) {
	p := Provider{Binary: "does-not-matter"}
	_, err := p.Run(context.Background(), provider.Request{Workspace: t.TempDir(), Approval: "on-request", Context: testInvocationContext("perform the task")})
	if err == nil {
		t.Fatal("expected approval policy error")
	}
}

func TestRenderInvocationContextIsDeterministicAndResolvesOnlyWorkspace(t *testing.T) {
	workspace := filepath.Join(string(filepath.Separator), "tmp", "actor-quarantine")
	context := testInvocationContext("Edit " + provider.WorkspacePlaceholder + "/src/result.txt and preserve /opt/reference.txt.")
	context.Artifacts = []provider.ArtifactReference{{
		Name: "result", Producer: "implement", Type: "files",
		Path: provider.WorkspacePlaceholder + "/src/result.txt", Digest: "abc", Mode: 0o100644,
	}}
	context.Validations = []provider.ValidationRequirement{{Name: "gate"}}

	first, err := RenderInvocationContext(context, workspace)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderInvocationContext(context, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("equivalent context rendered differently")
	}
	if strings.Contains(first, provider.WorkspacePlaceholder) || !strings.Contains(first, filepath.Join(workspace, "src", "result.txt")) || !strings.Contains(first, "/opt/reference.txt") {
		t.Fatalf("rendered context did not resolve only the workspace placeholder:\n%s", first)
	}
}

func TestValidateRequestRejectsMissingOrUnsupportedContextVersion(t *testing.T) {
	for _, version := range []string{"", "agentflow.dev/invocation-context/v999"} {
		t.Run(version, func(t *testing.T) {
			err := validateRequest(provider.Request{Context: provider.InvocationContext{Version: version}})
			if err == nil || !strings.Contains(err.Error(), "invocation context version") {
				t.Fatalf("validateRequest() error = %v", err)
			}
		})
	}
}
