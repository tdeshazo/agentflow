package engine

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/observability"
	"github.com/tdeshazo/agentflow-spec/provider"
	codexprovider "github.com/tdeshazo/agentflow-spec/provider/codex"
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
