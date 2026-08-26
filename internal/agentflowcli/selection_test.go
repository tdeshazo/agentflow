package agentflowcli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/observability"
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
	if err := runCurrentWorkflow(repo.Root, os.Stdout); err == nil || !strings.Contains(err.Error(), "invalid active workflow selection") {
		t.Fatalf("malformed selection error = %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Select("gone"); err != nil {
		t.Fatal(err)
	}
	if err := runWorkflows(repo.Root, os.Stdout); err == nil || !strings.Contains(err.Error(), "is stale") {
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

func TestWorkflowSwitchWithoutSelectorUsesPicker(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", "selected.yaml"), "selected")

	originalInteractive := workflowPickerInteractive
	originalHome := workflowHomeDirectory
	t.Cleanup(func() {
		workflowPickerInteractive = originalInteractive
		workflowHomeDirectory = originalHome
	})
	workflowPickerInteractive = func(io.Reader, io.Writer) bool { return true }
	workflowHomeDirectory = func() (string, error) { return home, nil }

	var output bytes.Buffer
	if err := runArgsWithIO([]string{"switch", "-C", repo.Root}, strings.NewReader("1\n"), &output); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "Select a workflow:\n1. selected (repository)\nEnter a number: selected\n"; got != want {
		t.Fatalf("switch picker output = %q, want %q", got, want)
	}
	selection, found, err := newSelectionStore(repo).Read()
	if err != nil || !found || selection.Current != "selected" {
		t.Fatalf("selection after picker = %+v, found %v, err %v", selection, found, err)
	}
}

func TestWorkflowSwitchWithoutSelectorFailsWithoutTerminal(t *testing.T) {
	repo := newCLIStatusRepo(t)
	originalInteractive := workflowPickerInteractive
	t.Cleanup(func() { workflowPickerInteractive = originalInteractive })
	workflowPickerInteractive = func(io.Reader, io.Writer) bool { return false }

	err := runArgsWithIO([]string{"switch", "-C", repo.Root}, &panicReader{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "workflow selector is required") || !strings.Contains(err.Error(), "agentflow switch workflow-name") {
		t.Fatalf("non-interactive switch error = %v", err)
	}
}

func TestCheckoutCurrentAndWorkflowsCommands(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	for _, selector := range []string{"beta", "alpha"} {
		writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", selector+".yaml"), selector)
	}
	originalHome := workflowHomeDirectory
	t.Cleanup(func() { workflowHomeDirectory = originalHome })
	workflowHomeDirectory = func() (string, error) { return home, nil }

	if err := runArgs([]string{"checkout", "alpha", "-C", repo.Root}); err != nil {
		t.Fatal(err)
	}
	var current bytes.Buffer
	if err := runArgsWithIO([]string{"current", "-C", repo.Root}, strings.NewReader(""), &current); err != nil {
		t.Fatal(err)
	}
	if got, want := current.String(), "alpha\n"; got != want {
		t.Fatalf("current output = %q, want %q", got, want)
	}

	var workflows bytes.Buffer
	if err := runArgsWithIO([]string{"workflows", "-C", repo.Root}, strings.NewReader(""), &workflows); err != nil {
		t.Fatal(err)
	}
	if got, want := workflows.String(), "* alpha\n  beta\n"; got != want {
		t.Fatalf("workflows output = %q, want %q", got, want)
	}

	if err := runArgs([]string{"checkout", "--clear", "-C", repo.Root}); err != nil {
		t.Fatal(err)
	}
	current.Reset()
	if err := runArgsWithIO([]string{"current", "-C", repo.Root}, strings.NewReader(""), &current); err != nil {
		t.Fatal(err)
	}
	if current.Len() != 0 {
		t.Fatalf("current without selection = %q", current.String())
	}
}

func TestCurrentReportsStoredStaleSelectionAndWorkflowsRejectsIt(t *testing.T) {
	repo := newCLIStatusRepo(t)
	if err := newSelectionStore(repo).Select("gone"); err != nil {
		t.Fatal(err)
	}

	var current bytes.Buffer
	if err := runArgsWithIO([]string{"current", "-C", repo.Root}, strings.NewReader(""), &current); err != nil {
		t.Fatal(err)
	}
	if got, want := current.String(), "gone\n"; got != want {
		t.Fatalf("current stale output = %q, want %q", got, want)
	}
	err := runArgsWithIO([]string{"workflows", "-C", repo.Root}, strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "is stale") {
		t.Fatalf("stale workflows error = %v", err)
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

func TestActiveSelectionFallsBackForEveryWorkflowCommand(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	workflowPath := filepath.Join(repo.Root, ".agentflow", "workflows", "active.yaml")
	writeCLIWorkflow(t, workflowPath, "active")
	store := newSelectionStore(repo)
	if err := store.Select("active"); err != nil {
		t.Fatal(err)
	}

	originalHome := workflowHomeDirectory
	originalStart := detachedStart
	t.Cleanup(func() {
		workflowHomeDirectory = originalHome
		detachedStart = originalStart
	})
	workflowHomeDirectory = func() (string, error) { return home, nil }

	statusOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--json", "-C", repo.Root})
	})
	if !strings.Contains(statusOutput, `"workflow":"active"`) {
		t.Fatalf("status using active selection = %q", statusOutput)
	}

	var validationOutput bytes.Buffer
	if err := runArgsWithIO([]string{"validate", "-C", repo.Root}, strings.NewReader(""), &validationOutput); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(validationOutput.String(), "valid and executable") {
		t.Fatalf("validation using active selection = %q", validationOutput.String())
	}

	planOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"plan", "--expanded", "-C", repo.Root})
	})
	if !strings.Contains(planOutput, "resolvedLifecycle:") {
		t.Fatalf("plan using active selection = %q", planOutput)
	}

	if err := runArgs([]string{"reset", "-C", repo.Root}); err != nil {
		t.Fatalf("reset using active selection: %v", err)
	}

	var childArgs []string
	detachedStart = func(cmd *exec.Cmd) error {
		childArgs = append([]string(nil), cmd.Args[1:]...)
		cmd.Process = &os.Process{Pid: 12345}
		return nil
	}
	if err := runArgs([]string{"run", "--detach", "-C", repo.Root}); err != nil {
		t.Fatalf("run using active selection: %v", err)
	}
	if !containsArgumentPair(childArgs, "-f", workflowPath) {
		t.Fatalf("detached active workflow args = %#v", childArgs)
	}

	if err := gitstate.NewStore(repo, "active").SetJSON(
		gitstate.DescriptorRecord,
		gitstate.NewDescriptor("active", "", gitstate.RecordNames{}),
	); err != nil {
		t.Fatal(err)
	}
	logStore, err := observability.Open(repo, "active")
	if err != nil {
		t.Fatal(err)
	}
	defer logStore.Close()
	if err := logStore.Event("phase_start", map[string]string{"phase": "active"}); err != nil {
		t.Fatal(err)
	}
	logsOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"logs", "-C", repo.Root})
	})
	if !strings.Contains(logsOutput, `"phase":"active"`) {
		t.Fatalf("logs using active selection = %q", logsOutput)
	}

	selection, found, err := store.Read()
	if err != nil || !found || selection.Current != "active" {
		t.Fatalf("active selection after one-off commands = %+v, found %v, err %v", selection, found, err)
	}
}

func TestLogsSelectorsOverrideActiveSelection(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	for _, selector := range []string{"active", "configured", "explicit"} {
		writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", selector+".yaml"), selector)
		if err := gitstate.NewStore(repo, selector).SetJSON(
			gitstate.DescriptorRecord,
			gitstate.NewDescriptor(selector, "", gitstate.RecordNames{}),
		); err != nil {
			t.Fatal(err)
		}
		logStore, err := observability.Open(repo, selector)
		if err != nil {
			t.Fatal(err)
		}
		if err := logStore.Event("phase_start", map[string]string{"phase": selector}); err != nil {
			t.Fatal(err)
		}
		if err := logStore.Close(); err != nil {
			t.Fatal(err)
		}
	}
	writeCLIConfig(t, repo.Root, "[logs]\nworkflow = \"configured\"\n")
	store := newSelectionStore(repo)
	if err := store.Select("active"); err != nil {
		t.Fatal(err)
	}

	originalHome := workflowHomeDirectory
	t.Cleanup(func() { workflowHomeDirectory = originalHome })
	workflowHomeDirectory = func() (string, error) { return home, nil }

	configuredOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"logs", "-C", repo.Root})
	})
	if !strings.Contains(configuredOutput, `"phase":"configured"`) {
		t.Fatalf("configured logs selector output = %q", configuredOutput)
	}

	explicitOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"logs", "--workflow", "explicit", "-C", repo.Root})
	})
	if !strings.Contains(explicitOutput, `"phase":"explicit"`) {
		t.Fatalf("explicit logs selector output = %q", explicitOutput)
	}

	selection, found, err := store.Read()
	if err != nil || !found || selection.Current != "active" {
		t.Fatalf("active selection after explicit logs selector = %+v, found %v, err %v", selection, found, err)
	}
}

func TestNoActiveSelectionPreservesInteractiveAndNonInteractiveResolution(t *testing.T) {
	t.Run("interactive picker", func(t *testing.T) {
		repo := newCLIStatusRepo(t)
		home := t.TempDir()
		writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", "selected.yaml"), "selected")
		if _, found, err := newSelectionStore(repo).Read(); err != nil || found {
			t.Fatalf("initial active selection = found %v, err %v", found, err)
		}

		originalInteractive := workflowPickerInteractive
		originalHome := workflowHomeDirectory
		t.Cleanup(func() {
			workflowPickerInteractive = originalInteractive
			workflowHomeDirectory = originalHome
		})
		workflowPickerInteractive = func(io.Reader, io.Writer) bool { return true }
		workflowHomeDirectory = func() (string, error) { return home, nil }

		var output bytes.Buffer
		err := runArgsWithIO([]string{"validate", "-C", repo.Root}, strings.NewReader("1\n"), &output)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Select a workflow:") ||
			!strings.Contains(output.String(), "valid and executable") {
			t.Fatalf("interactive no-active output = %q", output.String())
		}
	})

	t.Run("non-interactive selector error", func(t *testing.T) {
		repo := newCLIStatusRepo(t)
		if _, found, err := newSelectionStore(repo).Read(); err != nil || found {
			t.Fatalf("initial active selection = found %v, err %v", found, err)
		}

		read := &panicReader{}
		var output bytes.Buffer
		err := runArgsWithIO([]string{"validate", "-C", repo.Root}, read, &output)
		if err == nil || !strings.Contains(err.Error(), "-f workflow YAML is required") ||
			!strings.Contains(err.Error(), "workflow-name") {
			t.Fatalf("non-interactive no-active error = %v", err)
		}
	})
}

func TestStaleActiveSelectionGuidesSwitchOrClear(t *testing.T) {
	repo := newCLIStatusRepo(t)
	if err := newSelectionStore(repo).Select("gone"); err != nil {
		t.Fatal(err)
	}

	for _, command := range []string{"validate", "logs"} {
		t.Run(command, func(t *testing.T) {
			err := runArgs([]string{command, "-C", repo.Root})
			if err == nil || !strings.Contains(err.Error(), "is stale") ||
				!strings.Contains(err.Error(), "agentflow switch <workflow-name>") ||
				!strings.Contains(err.Error(), "agentflow switch --clear") {
				t.Fatalf("stale active selection error = %v", err)
			}
		})
	}
}

func containsArgumentPair(args []string, flag, value string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == flag && args[index+1] == value {
			return true
		}
	}
	return false
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
