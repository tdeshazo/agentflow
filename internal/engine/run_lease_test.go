package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestRunLeaseRejectsProgrammaticOwnerRecordCollision(t *testing.T) {
	tests := []struct {
		name    string
		records workflow.StateRecords
	}{
		{name: "scalar record", records: workflow.StateRecords{BaseCommit: runLeaseRecord}},
		{name: "integrity record", records: workflow.StateRecords{Integrity: map[string]string{"protected": "/" + runLeaseRecord}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Engine{Workflow: &workflow.Workflow{Spec: workflow.Spec{State: workflow.StateSpec{Records: tt.records}}}}
			if _, err := e.acquireRunLease(); err == nil || !strings.Contains(err.Error(), "conflicts with reserved runtime owner record") {
				t.Fatalf("owner record collision error = %v", err)
			}
		})
	}
}

func TestRunLeaseRejectsLiveOwnerAndRecoversVerifiedStaleOwner(t *testing.T) {
	repo := newLeaseRepository(t)
	store := gitstate.NewStore(repo, "lease-test")
	first := &Engine{Repo: repo, Store: store}
	lease, err := first.acquireRunLease()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.releaseRunLease(lease) })

	second := &Engine{Repo: repo, Store: store}
	if _, err := second.acquireRunLease(); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("live owner error = %v, want concurrent-owner rejection", err)
	}

	if _, err := store.DeleteIf(runLeaseRecord, lease); err != nil {
		t.Fatal(err)
	}
	if err := store.SetJSON(runLeaseRecord, RunLease{Version: 1, Process: &gitstate.ProcessMetadata{PID: 99999999, Start: "0"}}); err != nil {
		t.Fatal(err)
	}
	recovered, err := second.acquireRunLease()
	if err != nil {
		t.Fatalf("recover verified stale owner: %v", err)
	}
	second.releaseRunLease(recovered)
}

func TestRunLeaseReleaseCannotDeleteReplacement(t *testing.T) {
	repo := newLeaseRepository(t)
	store := gitstate.NewStore(repo, "lease-release")
	if err := store.SetJSON(runLeaseRecord, RunLease{Version: 1, Process: &gitstate.ProcessMetadata{PID: 99999999, Start: "0"}}); err != nil {
		t.Fatal(err)
	}
	owner := &Engine{Repo: repo, Store: store}
	old, err := owner.acquireRunLease()
	if err != nil {
		t.Fatal(err)
	}
	current := gitstate.CurrentProcessMetadata()
	if current == nil {
		t.Skip("stable process identity unavailable")
	}
	if err := store.SetJSON(runLeaseRecord, RunLease{Version: 1, Process: &gitstate.ProcessMetadata{PID: current.PID, Start: current.Start + "-replacement"}}); err != nil {
		t.Fatal(err)
	}
	owner.releaseRunLease(old)
	if _, ok, err := store.Resolve(runLeaseRecord); err != nil || !ok {
		t.Fatalf("replacement lease removed: ok=%v err=%v", ok, err)
	}
}

func newLeaseRepository(t *testing.T) gitstate.Repo {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "test"}, {"config", "user.email", "test@example.com"}} {
		if output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "-C", root, "add", "README.md").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, output)
	}
	if output, err := exec.Command("git", "-C", root, "commit", "-qm", "seed").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	return gitstate.Repo{Root: root}
}
