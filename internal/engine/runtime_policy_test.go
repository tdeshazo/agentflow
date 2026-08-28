package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestIntegrityBaselinePersistsPathDigestsWithoutContents(t *testing.T) {
	repo := newDurableRepo(t)
	protected := filepath.Join(repo, "protected", "rules.yaml")
	if err := os.MkdirAll(filepath.Dir(protected), 0o755); err != nil {
		t.Fatal(err)
	}
	const contents = "private-rule-contents\n"
	if err := os.WriteFile(protected, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	e := newDurableEngine(t, durableWorkflow(repo, "integrity-manifest"), &durableProvider{})
	rule := workflow.IntegrityRule{ID: "governance", Paths: []string{"protected/**"}, Mode: "exact-hash"}
	baseline, err := e.integrityRuleBaseline(rule, true)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Aggregate == "" || len(baseline.Paths["protected/rules.yaml"]) != 64 {
		t.Fatalf("integrity baseline = %#v", baseline)
	}
	encoded, err := json.Marshal(IntegrityBaseline{"governance": baseline})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), contents) || strings.Contains(string(encoded), strings.TrimSpace(contents)) {
		t.Fatalf("integrity baseline persisted file contents: %s", encoded)
	}
}

func TestAssertIntegrityReportsPathLevelChanges(t *testing.T) {
	repo := newDurableRepo(t)
	for path, contents := range map[string]string{
		"protected/changed.yaml": "before\n",
		"protected/removed.yaml": "remove\n",
	} {
		full := filepath.Join(repo, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := newDurableEngine(t, durableWorkflow(repo, "integrity-path-diff"), &durableProvider{})
	e.Workflow.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{ID: "governance", Paths: []string{"protected/**"}, Mode: "exact-hash"}}
	baseline, err := e.computeIntegrity()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.integrityRecord(), baseline); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "protected", "changed.yaml"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "protected", "added.yaml"), []byte("add\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "protected", "removed.yaml")); err != nil {
		t.Fatal(err)
	}

	err = e.assertIntegrity()
	var violation *safetyViolation
	if !errors.As(err, &violation) || violation.integrityViolation == nil {
		t.Fatalf("integrity error = %v, violation = %#v", err, violation)
	}
	got := violation.integrityViolation
	if got.IntegrityRule != "governance" ||
		!reflect.DeepEqual(got.Changed, []string{"protected/changed.yaml"}) ||
		!reflect.DeepEqual(got.Added, []string{"protected/added.yaml"}) ||
		!reflect.DeepEqual(got.Removed, []string{"protected/removed.yaml"}) {
		t.Fatalf("integrity violation = %#v", got)
	}
}

func TestAssertIntegrityAcceptsLegacyAggregateOnlyBaseline(t *testing.T) {
	repo := newDurableRepo(t)
	e := newDurableEngine(t, durableWorkflow(repo, "legacy-integrity"), &durableProvider{})
	rule := workflow.IntegrityRule{ID: "readme", Paths: []string{"README.md"}, Mode: "exact-hash"}
	e.Workflow.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{rule}
	digest, err := e.integrityHash(rule)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Store.SetJSON(e.integrityRecord(), map[string]string{"readme": digest}); err != nil {
		t.Fatal(err)
	}
	if err := e.assertIntegrity(); err != nil {
		t.Fatalf("unchanged legacy baseline: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = e.assertIntegrity()
	var violation *safetyViolation
	if !errors.As(err, &violation) || violation.integrityViolation != nil {
		t.Fatalf("legacy integrity error = %v, violation = %#v", err, violation)
	}
}

func TestStandaloneIntegrityFailureStatusIncludesPaths(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "standalone-integrity-status")
	w.Spec.Flow = nil
	w.Spec.Workspace.MutationPolicy.Integrity = []workflow.IntegrityRule{{ID: "readme", Paths: []string{"README.md"}, Mode: "exact-hash"}}
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "protected integrity rule readme changed") {
		t.Fatalf("integrity run error = %v", err)
	}
	var failure gitstate.FailureRecord
	if ok, err := e.Store.GetJSON(e.lastFailureRecord(), &failure); err != nil || !ok || failure.IntegrityViolation == nil {
		t.Fatalf("standalone integrity failure = %#v, ok=%t, err=%v", failure, ok, err)
	}
	var out bytes.Buffer
	e.Out = &out
	if err := e.Status(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "integrity_rule: readme") || !strings.Contains(out.String(), "changed:\n  - README.md") {
		t.Fatalf("standalone integrity text status = %s", out.String())
	}
	out.Reset()
	if err := e.StatusJSONTo(&out, false); err != nil {
		t.Fatal(err)
	}
	var status struct {
		IntegrityRule string   `json:"integrity_rule"`
		Changed       []string `json:"changed"`
		Added         []string `json:"added"`
		Removed       []string `json:"removed"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.IntegrityRule != "readme" || !reflect.DeepEqual(status.Changed, []string{"README.md"}) || status.Added == nil || status.Removed == nil {
		t.Fatalf("standalone integrity JSON status = %s", out.String())
	}
}

func TestIntegrityViolationPathsAreBounded(t *testing.T) {
	before := make(map[string]string)
	after := make(map[string]string)
	for i := 0; i < maxIntegrityPathsPerCategory+5; i++ {
		path := fmt.Sprintf("protected/%02d.yaml", i)
		before[path] = "before"
		after[path] = "after"
	}
	violation := diffIntegrityManifests("governance", before, after)
	if len(violation.Changed) != maxIntegrityPathsPerCategory {
		t.Fatalf("changed paths = %d, want %d", len(violation.Changed), maxIntegrityPathsPerCategory)
	}
}

func TestSafeRelativeIntegrityPathRejectsUnsafePresentationPaths(t *testing.T) {
	for _, tt := range []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "relative", path: "data/rules.yaml"},
		{name: "parent traversal", path: "../rules.yaml", wantErr: true},
		{name: "absolute", path: filepath.Join(string(filepath.Separator), "tmp", "rules.yaml"), wantErr: true},
		{name: "control character", path: "data/rules.yaml\nadded: injected", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := safeRelativeIntegrityPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("safeRelativeIntegrityPath(%q) error = %v, wantErr %t", tt.path, err, tt.wantErr)
			}
		})
	}
}

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
