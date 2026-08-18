package gitstate

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverDescriptorsProjectsMultipleNamespacesAndConfigurableRecords(t *testing.T) {
	repo := newDiscoveryRepo(t)
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	first := NewStore(repo, "release candidate")
	second := NewStore(repo, "release-candidate")
	firstDescriptor := NewDescriptor("release candidate", "/tmp/first.yaml", RecordNames{
		Base:             "state/base-commit",
		Branch:           "state/branch-name",
		ActivePhase:      "state/active-phase",
		WorkflowComplete: "state/finished",
	})
	secondDescriptor := NewDescriptor("release-candidate", "", RecordNames{})
	for _, tc := range []struct {
		store      Store
		descriptor Descriptor
	}{
		{first, firstDescriptor}, {second, secondDescriptor},
	} {
		if err := tc.store.SetJSON(DescriptorRecord, tc.descriptor); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.SetCommit("state/base-commit", head); err != nil {
		t.Fatal(err)
	}
	if err := first.SetJSON("state/branch-name", "feature/observability"); err != nil {
		t.Fatal(err)
	}
	if err := first.SetJSON("state/active-phase", map[string]any{
		"phase_id": "implement", "phase_start_commit": head, "actor_completed": false,
	}); err != nil {
		t.Fatal(err)
	}

	items, err := repo.DiscoverDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Workflow != "release candidate" || items[1].Workflow != "release-candidate" {
		t.Fatalf("discovered items = %#v", items)
	}
	status, err := items[0].Descriptor.ProjectStatus(repo, items[0].Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "active" || status.ActivePhase != "implement" || status.Branch != "feature/observability" || status.Base != head || status.Head != head {
		t.Fatalf("status = %+v", status)
	}
	if status.ProcessLiveness != "" {
		t.Fatalf("active record fabricated process liveness: %+v", status)
	}
}

func TestDiscoveryRetainsMalformedNamespaceAndDescriptor(t *testing.T) {
	repo := newDiscoveryRepo(t)
	valid := NewStore(repo, "valid")
	if err := valid.SetJSON(DescriptorRecord, NewDescriptor("valid", "", RecordNames{})); err != nil {
		t.Fatal(err)
	}
	if err := valid.SetJSON("active", map[string]string{"phase_id": "phase"}); err != nil {
		t.Fatal(err)
	}
	malformed := NewStore(repo, "malformed")
	if err := malformed.SetJSON(DescriptorRecord, map[string]any{
		"schema_version": DescriptorSchema + 1,
		"workflow":       "malformed",
		"records":        map[string]string{"base": "../escape"},
	}); err != nil {
		t.Fatal(err)
	}
	// A raw namespace with a path component must be reported, not decoded as a
	// workflow name or allowed to influence another namespace.
	if _, err := repo.run(nil, "update-ref", "refs/agentflow/workflow-zz/../bad", "HEAD"); err == nil {
		t.Fatal("Git unexpectedly accepted a traversal namespace")
	}
	items, err := repo.DiscoverDescriptors()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	for _, item := range items {
		if item.Workflow == "valid" && item.Descriptor == nil {
			t.Fatalf("valid workflow disappeared: %#v", item)
		}
		if item.Workflow == "malformed" && item.Error == "" {
			t.Fatalf("malformed workflow was not reported: %#v", item)
		}
	}
}

func TestDescriptorDoesNotPersistSensitiveInputs(t *testing.T) {
	repo := newDiscoveryRepo(t)
	store := NewStore(repo, "privacy")
	descriptor := NewDescriptor("privacy", "/tmp/workflow.yaml", RecordNames{})
	if err := store.SetJSON(DescriptorRecord, descriptor); err != nil {
		t.Fatal(err)
	}
	sha, ok, err := store.Resolve(DescriptorRecord)
	if err != nil || !ok {
		t.Fatalf("descriptor ref: %q %v %v", sha, ok, err)
	}
	b, err := repo.run(nil, "cat-file", "blob", sha)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"parameter-value", "ENV_SECRET", "prompt text", "canonical identity bytes"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("descriptor persisted %q: %s", secret, b)
		}
	}
	var decoded Descriptor
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Workflow != "privacy" {
		t.Fatalf("descriptor = %+v", decoded)
	}
}

func TestWorkflowNameEncodingRejectsTraversalNamespace(t *testing.T) {
	name := "../../release candidate"
	namespace := NamespaceForWorkflow(name)
	decoded, err := WorkflowNameFromNamespace(namespace)
	if err != nil || decoded != name {
		t.Fatalf("decoded = %q, err = %v", decoded, err)
	}
	if _, err := WorkflowNameFromNamespace(namespace + "/../escape"); err == nil {
		t.Fatal("accepted namespace path traversal")
	}
	if filepath.IsAbs(namespace) {
		t.Fatalf("namespace became absolute path: %q", namespace)
	}
}

func TestStatusProjectionRejectsNonAuthoritativeAcceptanceShapes(t *testing.T) {
	repo := newDiscoveryRepo(t)
	store := NewStore(repo, "malformed-acceptance")
	if err := store.SetJSON(DescriptorRecord, NewDescriptor("malformed-acceptance", "", RecordNames{})); err != nil {
		t.Fatal(err)
	}
	if err := store.SetJSON("base", map[string]string{"not": "a commit"}); err != nil {
		t.Fatal(err)
	}
	item, found, err := repo.FindDescriptor("malformed-acceptance")
	if err != nil || !found || item.Descriptor == nil {
		t.Fatalf("descriptor = %#v, found=%v, err=%v", item, found, err)
	}
	if _, err := item.Descriptor.ProjectStatus(repo, item.Namespace); err == nil {
		t.Fatal("status projected a JSON blob as the base commit")
	}

	if err := store.SetCommit("base", mustHead(t, repo)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetJSON("active", map[string]any{"actor_completed": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := item.Descriptor.ProjectStatus(repo, item.Namespace); err == nil {
		t.Fatal("status projected an active record without a phase id")
	}
}

func TestProcessLivenessRequiresVerifiedStartMetadata(t *testing.T) {
	metadata := CurrentProcessMetadata()
	if metadata == nil {
		t.Skip("process start metadata is unavailable on this host")
	}
	if got, verified := ProcessLiveness(metadata); !verified || got != "running" {
		t.Fatalf("current process liveness = %q, verified=%v", got, verified)
	}
	stale := *metadata
	stale.Start += "-stale"
	if got, verified := ProcessLiveness(&stale); !verified || got != "not_running" {
		t.Fatalf("stale process liveness = %q, verified=%v", got, verified)
	}
}

func newDiscoveryRepo(t *testing.T) Repo {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	git("init", "-q")
	git("config", "user.name", "test")
	git("config", "user.email", "test@example.com")
	git("commit", "--allow-empty", "-qm", "init")
	return Repo{Root: dir}
}

func mustHead(t *testing.T, repo Repo) string {
	t.Helper()
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	return head
}
