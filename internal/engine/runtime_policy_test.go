package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestIntegrityHashIncludesIgnoredFiles(t *testing.T) {
	repo := newDurableRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".agentflow/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".agentflow"), 0o755); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(repo, ".agentflow", "workflow.yaml")
	if err := os.WriteFile(protected, []byte("version: one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".gitignore")
	gitIn(t, repo, "commit", "-qm", "ignore local controls")

	e := newDurableEngine(t, durableWorkflow(repo, "ignored-integrity"), &durableProvider{})
	rule := workflow.IntegrityRule{ID: "workflow", Paths: []string{".agentflow/workflow.yaml"}, Mode: "exact-hash"}
	before, err := e.integrityHash(rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(protected, []byte("version: two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := e.integrityHash(rule)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("ignored protected file change did not change integrity hash")
	}
}

func TestIntegrityHashFailsClosedWhenPathsMatchNothing(t *testing.T) {
	repo := newDurableRepo(t)
	e := newDurableEngine(t, durableWorkflow(repo, "empty-integrity"), &durableProvider{})
	_, err := e.integrityHash(workflow.IntegrityRule{ID: "missing", Paths: []string{"missing/**"}, Mode: "exact-hash"})
	if err == nil || !strings.Contains(err.Error(), "matched no workspace files") {
		t.Fatalf("integrity error = %v, want zero-match failure", err)
	}
}

func TestIntegrityHashDoesNotFollowSymlinkTargets(t *testing.T) {
	repo := newDurableRepo(t)
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, "control-link")
	if err := os.Symlink(external, link); err != nil {
		t.Fatal(err)
	}

	e := newDurableEngine(t, durableWorkflow(repo, "symlink-integrity"), &durableProvider{})
	rule := workflow.IntegrityRule{ID: "link", Paths: []string{"control-link"}, Mode: "exact-hash"}
	before, err := e.integrityHash(rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := e.integrityHash(rule)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("integrity hash followed and hashed an external symlink target")
	}
}
