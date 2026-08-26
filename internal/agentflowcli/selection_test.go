package agentflowcli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

func TestSelectionStoreReadWriteAndReplace(t *testing.T) {
	repo := newCLIStatusRepo(t)
	store := newSelectionStore(repo)

	if _, found, err := store.Read(); err != nil || found {
		t.Fatalf("initial selection = found %v, err %v", found, err)
	}
	if err := store.Select("first"); err != nil {
		t.Fatal(err)
	}
	selection, found, err := store.Read()
	if err != nil || !found {
		t.Fatalf("read first selection = found %v, err %v", found, err)
	}
	if selection.Current != "first" || selection.Previous != "" {
		t.Fatalf("first selection = %+v", selection)
	}

	if err := store.Select("second"); err != nil {
		t.Fatal(err)
	}
	selection, found, err = store.Read()
	if err != nil || !found {
		t.Fatalf("read replacement = found %v, err %v", found, err)
	}
	if selection.Current != "second" || selection.Previous != "first" {
		t.Fatalf("replacement selection = %+v", selection)
	}
	assertSelectionDoesNotDirtyWorkspace(t, repo)
}

func TestSelectionStoreSwitchPrevious(t *testing.T) {
	repo := newCLIStatusRepo(t)
	store := newSelectionStore(repo)
	if err := store.Select("first"); err != nil {
		t.Fatal(err)
	}
	if err := store.Select("second"); err != nil {
		t.Fatal(err)
	}

	current, err := store.SwitchPrevious()
	if err != nil {
		t.Fatal(err)
	}
	if current != "first" {
		t.Fatalf("switch - = %q, want first", current)
	}
	selection, found, err := store.Read()
	if err != nil || !found || selection.Current != "first" || selection.Previous != "second" {
		t.Fatalf("selection after switch - = %+v, found %v, err %v", selection, found, err)
	}
}

func TestSelectionStoreClear(t *testing.T) {
	repo := newCLIStatusRepo(t)
	store := newSelectionStore(repo)
	if err := store.Select("first"); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Read(); err != nil || found {
		t.Fatalf("selection after clear = found %v, err %v", found, err)
	}
	assertSelectionDoesNotDirtyWorkspace(t, repo)
}

func TestSelectionStoreUsesGitPathInLinkedWorktree(t *testing.T) {
	mainRepo := newCLIStatusRepo(t)
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runSelectionGit(t, mainRepo.Root, "worktree", "add", "-q", "-b", "selection-linked", linkedRoot)
	linkedRepo := gitstate.Repo{Root: linkedRoot}

	mainStore := newSelectionStore(mainRepo)
	linkedStore := newSelectionStore(linkedRepo)
	mainPath, err := mainStore.path()
	if err != nil {
		t.Fatal(err)
	}
	linkedPath, err := linkedStore.path()
	if err != nil {
		t.Fatal(err)
	}
	if mainPath == linkedPath {
		t.Fatalf("main and linked worktree state paths are shared: %q", mainPath)
	}
	if strings.HasPrefix(linkedPath, filepath.Join(linkedRoot, ".git")+string(filepath.Separator)) {
		t.Fatalf("linked worktree selection path assumes .git is a directory: %q", linkedPath)
	}
	if err := linkedStore.Select("linked"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := mainStore.Read(); err != nil || found {
		t.Fatalf("main worktree selection = found %v, err %v", found, err)
	}
	selection, found, err := linkedStore.Read()
	if err != nil || !found || selection.Current != "linked" {
		t.Fatalf("linked worktree selection = %+v, found %v, err %v", selection, found, err)
	}
	assertSelectionDoesNotDirtyWorkspace(t, linkedRepo)
}

func TestWorkflowSwitchRejectsMalformedAndStaleSelection(t *testing.T) {
	repo := newCLIStatusRepo(t)
	store := newSelectionStore(repo)
	path, err := store.path()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"current":"nested/workflow"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowSwitch(repo.Root, nil, false, os.Stdout); err == nil || !strings.Contains(err.Error(), "invalid active workflow selection") {
		t.Fatalf("malformed selection error = %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Select("gone"); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowSwitch(repo.Root, nil, false, os.Stdout); err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("stale selection error = %v", err)
	}
}

func TestWorkflowSwitchPreviousUsesLogicalSelectors(t *testing.T) {
	repo := newCLIStatusRepo(t)
	for _, selector := range []string{"first", "second"} {
		writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", selector+".yaml"), selector)
	}

	if err := runWorkflowSwitch(repo.Root, []string{"first"}, false, os.Stdout); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowSwitch(repo.Root, []string{"second"}, false, os.Stdout); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflowSwitch(repo.Root, []string{"-"}, false, os.Stdout); err != nil {
		t.Fatal(err)
	}
	selection, found, err := newSelectionStore(repo).Read()
	if err != nil || !found || selection.Current != "first" || selection.Previous != "second" {
		t.Fatalf("selection after CLI switch - = %+v, found %v, err %v", selection, found, err)
	}
}

func TestActiveSelectionIsAWorkflowCommandDefault(t *testing.T) {
	repo := newCLIStatusRepo(t)
	writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", "selected.yaml"), "selected-workflow")
	if err := newSelectionStore(repo).Select("selected"); err != nil {
		t.Fatal(err)
	}

	output := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--json", "-C", repo.Root})
	})
	if !strings.Contains(output, `"workflow":"selected-workflow"`) {
		t.Fatalf("status using active selection = %q", output)
	}
}

func assertSelectionDoesNotDirtyWorkspace(t *testing.T, repo gitstate.Repo) {
	t.Helper()
	cmd := exec.Command("git", "-C", repo.Root, "status", "--porcelain")
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != 0 {
		t.Fatalf("selection dirtied implementation workspace: %q", output)
	}
}

func runSelectionGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
