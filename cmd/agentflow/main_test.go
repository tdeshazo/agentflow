package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/observability"
)

func TestPlanExpandedCLI(t *testing.T) {
	workflowFile := filepath.Join("..", "..", "internal", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	runErr := runArgs([]string{"plan", "--expanded", "-f", workflowFile})
	_ = write.Close()
	os.Stdout = original
	output, _ := io.ReadAll(read)
	_ = read.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if strings.Contains(string(output), "\x1b[") {
		t.Fatalf("plan YAML contains ANSI escapes: %q", output)
	}
	for _, want := range []string{"resolvedLifecycle:", "safetyEnforcementPoints:", "recoveryBehavior:", "completionContract:"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("plan output missing %q:\n%s", want, output)
		}
	}
}

func TestStatusJSONCLI(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "AgentFlow Test"},
		{"config", "user.email", "agentflow@example.invalid"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	workflowFile := filepath.Join(filepath.Dir(source), "..", "..", "internal", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = write
	runErr := runArgs([]string{"status", "--json", "-f", workflowFile, "-C", repo})
	_ = write.Close()
	os.Stdout = originalStdout
	output, readErr := io.ReadAll(read)
	_ = read.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.HasSuffix(string(output), "\n") || strings.Contains(strings.TrimSuffix(string(output), "\n"), "\n") {
		t.Fatalf("redirected single-workflow JSON was not compact: %q", output)
	}
	if strings.Contains(string(output), "\x1b[") {
		t.Fatalf("status JSON contains ANSI escapes: %q", output)
	}

	var status map[string]any
	if err := json.Unmarshal(output, &status); err != nil {
		t.Fatalf("CLI status JSON = %q: %v", output, err)
	}
	if status["state"] != "uninitialized" || status["initialized"] != false {
		t.Fatalf("CLI status JSON = %v", status)
	}
}

func TestStatusAllCLITextAndJSON(t *testing.T) {
	repo := newCLIStatusRepo(t)
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"alpha", "beta"} {
		store := gitstate.NewStore(repo, name)
		if err := store.SetJSON(gitstate.DescriptorRecord, gitstate.NewDescriptor(name, "", gitstate.RecordNames{})); err != nil {
			t.Fatal(err)
		}
		if err := store.SetCommit("base", head); err != nil {
			t.Fatal(err)
		}
		if err := store.SetJSON("branch", "main"); err != nil {
			t.Fatal(err)
		}
	}
	textOutput := captureCLIStdout(t, func() error { return runArgs([]string{"status", "--all", "-C", repo.Root}) })
	if !strings.Contains(textOutput, "workflow: alpha") || !strings.Contains(textOutput, "workflow: beta") || !strings.Contains(textOutput, "state: ready") {
		t.Fatalf("status --all text = %s", textOutput)
	}
	if strings.Contains(textOutput, "{\n") {
		t.Fatalf("non-JSON status was pretty-printed as JSON: %s", textOutput)
	}
	jsonOutput := captureCLIStdout(t, func() error { return runArgs([]string{"status", "--all", "--json", "-C", repo.Root}) })
	if !strings.HasSuffix(jsonOutput, "\n") || strings.Contains(strings.TrimSuffix(jsonOutput, "\n"), "\n") {
		t.Fatalf("redirected repository-wide JSON was not compact: %q", jsonOutput)
	}
	var collection struct {
		SchemaVersion int                         `json:"schema_version"`
		Repo          string                      `json:"repo"`
		Workflows     []gitstate.StatusProjection `json:"workflows"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &collection); err != nil {
		t.Fatalf("status --all JSON = %q: %v", jsonOutput, err)
	}
	if collection.SchemaVersion != 1 || collection.Repo != repo.Root || len(collection.Workflows) != 2 {
		t.Fatalf("status --all collection = %+v", collection)
	}
}

func TestStatusJSONUsesSameTTYPolicyForSingleAndAll(t *testing.T) {
	repo := newCLIStatusRepo(t)
	workflowFile := filepath.Join("..", "..", "internal", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	originalDetector := statusOutputIsTTY
	t.Cleanup(func() { statusOutputIsTTY = originalDetector })

	statusOutputIsTTY = func(io.Writer) bool { return true }
	prettySingle := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--json", "-f", workflowFile, "-C", repo.Root})
	})
	prettyAll := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--all", "--json", "-C", repo.Root})
	})

	statusOutputIsTTY = func(io.Writer) bool { return false }
	compactSingle := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--json", "-f", workflowFile, "-C", repo.Root})
	})
	compactAll := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--all", "--json", "-C", repo.Root})
	})

	assertJSONFormattingAndEquivalentData(t, prettySingle, compactSingle)
	assertJSONFormattingAndEquivalentData(t, prettyAll, compactAll)
}

func TestStatusAllReportsOnlyVerifiedProcessLiveness(t *testing.T) {
	repo := newCLIStatusRepo(t)
	metadata := gitstate.CurrentProcessMetadata()
	if metadata == nil {
		t.Skip("process start metadata is unavailable on this host")
	}
	descriptor := gitstate.NewDescriptor("live", "", gitstate.RecordNames{})
	descriptor.Process = metadata
	store := gitstate.NewStore(repo, "live")
	if err := store.SetJSON(gitstate.DescriptorRecord, descriptor); err != nil {
		t.Fatal(err)
	}
	var collection statusAllOutput
	output := captureCLIStdout(t, func() error { return runArgs([]string{"status", "--all", "--json", "-C", repo.Root}) })
	if err := json.Unmarshal([]byte(output), &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Workflows) != 1 || collection.Workflows[0].ProcessLiveness != "running" {
		t.Fatalf("verified liveness = %+v", collection.Workflows)
	}

	descriptor.Process.Start += "-stale"
	if err := store.SetJSON(gitstate.DescriptorRecord, descriptor); err != nil {
		t.Fatal(err)
	}
	output = captureCLIStdout(t, func() error { return runArgs([]string{"status", "--all", "--json", "-C", repo.Root}) })
	if err := json.Unmarshal([]byte(output), &collection); err != nil {
		t.Fatal(err)
	}
	if len(collection.Workflows) != 1 || collection.Workflows[0].ProcessLiveness != "not_running" {
		t.Fatalf("stale liveness = %+v", collection.Workflows)
	}
}

func TestStatusAllRejectsWorkflowFileSelector(t *testing.T) {
	if err := runArgs([]string{"status", "--all", "-f", "workflow.yaml"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("selector conflict error = %v", err)
	}
}

func TestDetachAcceptedOnlyForRun(t *testing.T) {
	for _, command := range []string{"validate", "plan", "status", "logs", "reset"} {
		t.Run(command, func(t *testing.T) {
			err := runArgs([]string{command, "--detach"})
			if err == nil || !strings.Contains(err.Error(), command+" does not support --detach") {
				t.Fatalf("--detach error = %v", err)
			}
		})
	}
}

func TestForegroundRunDoesNotTakeDetachedPath(t *testing.T) {
	original := detachedStart
	t.Cleanup(func() { detachedStart = original })
	called := false
	detachedStart = func(*exec.Cmd) error {
		called = true
		return errors.New("detached path was used")
	}
	if err := runArgs([]string{"run"}); err == nil || !strings.Contains(err.Error(), "-f workflow YAML is required") {
		t.Fatalf("foreground run error = %v", err)
	}
	if called {
		t.Fatal("foreground run used detached launcher")
	}
}

func TestLogsCLIRejectsNegativeTailAndReportsUnknownWorkflow(t *testing.T) {
	if err := runArgs([]string{"logs", "--workflow", "x", "--tail", "-1"}); err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Fatalf("negative tail error = %v", err)
	}
	repo := newCLIStatusRepo(t)
	if err := runLogs(repo.Root, "unknown", -1, false); err == nil || !strings.Contains(err.Error(), "unknown workflow") {
		t.Fatalf("unknown workflow error = %v", err)
	}
	store := gitstate.NewStore(repo, "no-log")
	if err := store.SetJSON(gitstate.DescriptorRecord, gitstate.NewDescriptor("no-log", "", gitstate.RecordNames{})); err != nil {
		t.Fatal(err)
	}
	if err := runLogs(repo.Root, "no-log", -1, false); err == nil || !strings.Contains(err.Error(), "no logs") {
		t.Fatalf("no-log error = %v", err)
	}
	withLog := gitstate.NewStore(repo, "with-log")
	if err := withLog.SetJSON(gitstate.DescriptorRecord, gitstate.NewDescriptor("with-log", "", gitstate.RecordNames{})); err != nil {
		t.Fatal(err)
	}
	logStore, err := observability.Open(repo, "with-log")
	if err != nil {
		t.Fatal(err)
	}
	defer logStore.Close()
	for _, phase := range []string{"one", "two"} {
		if err := logStore.Event("phase_start", map[string]string{"phase": phase}); err != nil {
			t.Fatal(err)
		}
	}
	output := captureCLIStdout(t, func() error { return runLogs(repo.Root, "with-log", 1, false) })
	if !strings.Contains(output, `"phase":"two"`) || strings.Contains(output, `"phase":"one"`) {
		t.Fatalf("tail output = %s", output)
	}
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		if strings.HasPrefix(line, " ") {
			t.Fatalf("logs output was JSON pretty-printed: %q", output)
		}
	}
}

func newCLIStatusRepo(t *testing.T) gitstate.Repo {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "AgentFlow Test"},
		{"config", "user.email", "agentflow@example.invalid"},
		{"commit", "--allow-empty", "-qm", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	return gitstate.Repo{Root: dir}
}

func captureCLIStdout(t *testing.T, fn func() error) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	callErr := fn()
	_ = write.Close()
	os.Stdout = original
	data, readErr := io.ReadAll(read)
	_ = read.Close()
	if callErr != nil {
		t.Fatal(callErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(data)
}

func assertJSONFormattingAndEquivalentData(t *testing.T, pretty, compact string) {
	t.Helper()
	if !strings.HasSuffix(pretty, "\n") || !strings.HasSuffix(compact, "\n") {
		t.Fatalf("JSON output missing final newline: pretty=%q compact=%q", pretty, compact)
	}
	prettyBody := strings.TrimSuffix(pretty, "\n")
	compactBody := strings.TrimSuffix(compact, "\n")
	if !strings.Contains(prettyBody, "\n") {
		t.Fatalf("TTY JSON was not indented: %q", pretty)
	}
	if strings.Contains(compactBody, "\n") {
		t.Fatalf("non-TTY JSON was not one line: %q", compact)
	}
	var prettyValue, compactValue any
	if err := json.Unmarshal([]byte(prettyBody), &prettyValue); err != nil {
		t.Fatalf("pretty JSON is invalid: %q: %v", pretty, err)
	}
	if err := json.Unmarshal([]byte(compactBody), &compactValue); err != nil {
		t.Fatalf("compact JSON is invalid: %q: %v", compact, err)
	}
	if !reflect.DeepEqual(prettyValue, compactValue) {
		t.Fatalf("pretty and compact JSON differ: pretty=%v compact=%v", prettyValue, compactValue)
	}
}
