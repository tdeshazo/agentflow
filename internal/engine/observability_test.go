package engine

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/observability"
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
