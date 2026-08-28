package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/observability"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
	codexprovider "github.com/tdeshazo/agentflow/provider/codex"
)

func TestRunCreatesDescriptorAndDurableOperationalLog(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "observability-run")
	w.Spec.Flow = nil
	e := newDurableEngine(t, w, &durableProvider{})
	e.Out = io.Discard
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	discovery, found, err := e.Repo.FindDescriptor(w.Metadata.Name)
	if err != nil || !found || discovery.Descriptor == nil {
		t.Fatalf("descriptor discovery = %#v, found=%v, err=%v", discovery, found, err)
	}
	if discovery.Descriptor.Process != nil {
		t.Fatalf("normal process left stale liveness metadata: %+v", discovery.Descriptor.Process)
	}
	data, path, err := observability.Read(e.Repo, w.Metadata.Name)
	if err != nil {
		t.Fatalf("read log %q: %v", path, err)
	}
	for _, event := range []string{"workflow_start", "workflow_end"} {
		if !strings.Contains(string(data), `"event":"`+event+`"`) {
			t.Fatalf("log missing %s: %s", event, data)
		}
	}
	if strings.Contains(string(data), "run-identity") {
		t.Fatalf("log persisted identity material: %s", data)
	}
	if _, ok, err := e.Store.Resolve(gitstate.DescriptorRecord); err != nil || !ok {
		t.Fatalf("descriptor record = ok %v, err %v", ok, err)
	}
}

func TestCompletionFailureIsDurableAndClearedAfterSuccessfulRetry(t *testing.T) {
	repo := newDurableRepo(t)
	statePath := filepath.Join(repo, "state.txt")
	if err := os.WriteFile(statePath, []byte("pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "state.txt")
	gitIn(t, repo, "commit", "-qm", "seed state")
	pattern := strings.Repeat("x", maxValidationFailureOutput+1024)
	w := &workflow.Workflow{
		APIVersion: "agentflow.dev/v1alpha1",
		Kind:       "AgentWorkflow",
		Metadata:   workflow.Metadata{Name: "observable-completion-failure"},
		Spec: workflow.Spec{
			Workspace: workflow.WorkspaceSpec{Root: repo, MutationPolicy: workflow.MutationPolicy{Allowed: []string{"state.txt"}}},
			Tools:     map[string]workflow.Tool{"checked": {Type: "file-regex"}},
			Flow:      []workflow.FlowStep{{Complete: "default"}},
			Completion: map[string]workflow.Completion{"default": {Assertions: []workflow.Assertion{{
				Uses: "checked", With: workflow.ToolArguments{Path: "state.txt", Regex: "^" + pattern + "$"},
			}}}},
		},
	}
	first := newCompletionRegressionEngine(t, w, &completionRegressionProvider{})
	if err := first.Run(context.Background()); err == nil {
		t.Fatal("completion unexpectedly succeeded")
	}
	snapshot, err := first.statusSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "failed/retryable" || snapshot.FailureStage != "completion/default" || !strings.Contains(snapshot.LastError, "does not match") {
		t.Fatalf("failed status = %+v", snapshot)
	}
	if len(snapshot.LastError) > maxValidationFailureOutput+64 || !strings.Contains(snapshot.LastError, "[validation output truncated]") {
		t.Fatalf("last error was not bounded: length=%d suffix=%q", len(snapshot.LastError), snapshot.LastError[max(0, len(snapshot.LastError)-64):])
	}
	data, _, err := observability.Read(first.Repo, w.Metadata.Name)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"event":"completion_end"`, `"stage":"completion/default"`, `"error":`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("failure log missing %s: %s", want, data)
		}
	}

	if err := os.WriteFile(statePath, []byte(pattern+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "state.txt")
	gitIn(t, repo, "commit", "-qm", "check state")
	restarted := newCompletionRegressionEngine(t, w, &completionRegressionProvider{})
	if err := restarted.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err = restarted.statusSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "completed" || snapshot.FailureStage != "" || snapshot.LastError != "" {
		t.Fatalf("successful retry retained failure = %+v", snapshot)
	}
}

func TestDetachedCodexOutputUsesPlainCapturedPresentation(t *testing.T) {
	repo := newDurableRepo(t)
	fake := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
color=never
while [ "$#" -gt 0 ]; do
    case "$1" in
        --color) color="$2"; shift 2 ;;
        --output-last-message) printf 'complete' > "$2"; shift 2 ;;
        *) shift ;;
    esac
done
if [ "$color" = always ]; then
    printf '\033[31mprovider stdout\033[0m\n'
    printf '\033[33mprovider diagnostic\033[0m\n' >&2
else
    printf 'provider stdout\n'
    printf 'provider diagnostic\n' >&2
fi
printf 'complete\n' > work.txt
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}

	w := durableWorkflow(repo, "detached-codex-output")
	agent := w.Spec.Agents["worker"]
	agent.Runner = "codex"
	agent.Color = "always"
	w.Spec.Agents["worker"] = agent
	e, err := New(w, map[string]provider.Provider{
		"codex": codexprovider.Provider{
			Binary: fake,
			OutputTTY: func(io.Writer) bool {
				// Exercise the engine-owned detached boundary even if an adapter
				// reports a terminal-like destination.
				return true
			},
		},
	}, Options{Detached: true})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	data, _, err := observability.Read(e.Repo, w.Metadata.Name)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"provider stdout", "provider diagnostic"} {
		if !strings.Contains(text, want) {
			t.Fatalf("captured log missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("captured Codex output contains terminal presentation escapes: %s", text)
	}
}
