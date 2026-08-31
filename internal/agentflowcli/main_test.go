package agentflowcli

import (
	"bytes"
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

	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/observability"
)

func TestValidateCLIRejectsInvalidReferencesBeforeRepositoryAccess(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "invalid", "unknown-references.yaml")
	missingRepository := filepath.Join(t.TempDir(), "not-a-repository")
	var output bytes.Buffer
	err := runArgsWithIO([]string{"validate", "-C", missingRepository, "-f", workflowFile}, strings.NewReader(""), &output)
	if err == nil || !strings.Contains(err.Error(), "workflow is invalid") {
		t.Fatalf("validate error = %v, want invalid workflow", err)
	}
	if !strings.Contains(output.String(), "spec.phases[0].actor") || !strings.Contains(output.String(), "invalid") {
		t.Fatalf("validate output = %q, want source-aware invalid diagnostic", output.String())
	}
}

func TestRunCLIRejectsInvalidReferencesBeforeRepositoryAccess(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "invalid", "unknown-references.yaml")
	missingRepository := filepath.Join(t.TempDir(), "not-a-repository")
	err := runArgsWithIO([]string{"run", "-C", missingRepository, "-f", workflowFile}, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "spec.phases[0].actor") || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("run error = %v, want source-aware validation failure before repository access", err)
	}
}

func TestValidateCLIRejectsNonExecutableRecoveryBeforeRepositoryAccess(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "unsupported", "active-phase-recovery.yaml")
	missingRepository := filepath.Join(t.TempDir(), "not-a-repository")
	var output bytes.Buffer
	err := runArgsWithIO([]string{"validate", "-C", missingRepository, "-f", workflowFile}, strings.NewReader(""), &output)
	if err != nil {
		t.Fatalf("validate error = %v, want source-only unsupported result", err)
	}
	if !strings.Contains(output.String(), "spec.recovery.activePhase") || !strings.Contains(output.String(), "unsupported") {
		t.Fatalf("validate output = %q, want source-aware unsupported diagnostic", output.String())
	}
}

func TestPlanExpandedCLI(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	var output bytes.Buffer
	if err := runArgsWithIO(
		[]string{"plan", "--expanded", "-f", workflowFile},
		strings.NewReader(""),
		&output,
	); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "\x1b[") {
		t.Fatalf("plan YAML contains ANSI escapes: %q", output.String())
	}
	for _, want := range []string{"resolvedLifecycle:", "safetyEnforcementPoints:", "recoveryBehavior:", "contextRecipes:", "runtimeResolved: true", "artifact contents", "completionContract:"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("plan output missing %q:\n%s", want, output.String())
		}
	}
}

func TestPlanExpandedCLIExposesV1Alpha2DependencyGraph(t *testing.T) {
	workflowFile := filepath.Join(t.TempDir(), "workflow.yaml")
	body := `
apiVersion: agentflow.dev/v1alpha2
kind: AgentWorkflow
metadata: {name: dependency-plan}
spec:
  workspace: {allowWrites: [src/**]}
  agents:
    coder: {runner: codex, model: gpt-5.6-terra}
    reviewer: {runner: codex, model: gpt-5.6-luna}
  validation: {tests: {run: "true"}}
  phases:
    - {id: implement, actor: coder, prompt: implement, validation: tests}
    - {id: review, actor: reviewer, prompt: review, validation: tests, dependsOn: [implement]}
  completion: {validation: tests}
`
	if err := os.WriteFile(workflowFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runArgsWithIO(
		[]string{"plan", "--expanded", "-f", workflowFile},
		strings.NewReader(""),
		&output,
	); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"dependencyGraph:",
		"authoredOrder: 0",
		"phase: review",
		"dependsOn: implement",
		"satisfiedWhen: deterministically accepted",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("plan output missing %q:\n%s", want, output.String())
		}
	}
}

func TestMigrateCheckCLIReportsClassificationsWithoutRepositoryAccess(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	var output bytes.Buffer
	if err := runArgsWithIO(
		[]string{"migrate", "--check", "-f", workflowFile},
		strings.NewReader(""),
		&output,
	); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"status: supported-maintenance-frozen",
		"path: spec.agents.worker.runner",
		"classification: direct-successor-capability",
		"classification: generalized-replacement",
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("migration output missing %q:\n%s", want, output.String())
		}
	}
}

func TestMigrateRequiresCheck(t *testing.T) {
	if err := runArgs([]string{"migrate", "-f", "workflow.yaml"}); err == nil || !strings.Contains(err.Error(), "requires --check") {
		t.Fatalf("migrate error = %v", err)
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
	workflowFile := filepath.Join(filepath.Dir(source), "..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
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
		if name == "beta" {
			if err := store.SetJSON("active", map[string]any{
				"phase_id":           "implement",
				"phase_start_commit": head,
				"failure_kind":       "safety",
				"integrity_violation": map[string]any{
					"integrity_rule": "roadmap-and-rules-governance",
					"changed":        []string{"data/mothership/v1.2/rules.yaml"},
					"added":          []string{},
					"removed":        []string{},
				},
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	textOutput := captureCLIStdout(t, func() error { return runArgs([]string{"status", "--all", "-C", repo.Root}) })
	if !strings.Contains(textOutput, "workflow: alpha") || !strings.Contains(textOutput, "workflow: beta") || !strings.Contains(textOutput, "state: ready") || !strings.Contains(textOutput, "recovery: operator-action-required") || !strings.Contains(textOutput, "next_action: reset-or-abandon") {
		t.Fatalf("status --all text = %s", textOutput)
	}
	for _, want := range []string{"integrity_rule: roadmap-and-rules-governance", "changed:\n    - data/mothership/v1.2/rules.yaml", "added: []", "removed: []"} {
		if !strings.Contains(textOutput, want) {
			t.Fatalf("status --all text missing %q: %s", want, textOutput)
		}
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
	if collection.Workflows[1].State != "safety-failed/terminal" || collection.Workflows[1].Recovery != "operator-action-required" || collection.Workflows[1].NextAction != "reset-or-abandon" {
		t.Fatalf("status --all recovery metadata = %+v", collection.Workflows[1])
	}
	integrity := collection.Workflows[1].IntegrityViolation
	if integrity == nil || integrity.IntegrityRule != "roadmap-and-rules-governance" || !reflect.DeepEqual(integrity.Changed, []string{"data/mothership/v1.2/rules.yaml"}) || integrity.Added == nil || integrity.Removed == nil {
		t.Fatalf("status --all integrity metadata = %+v", collection.Workflows[1])
	}
}

func TestStatusJSONUsesSameTTYPolicyForSingleAndAll(t *testing.T) {
	repo := newCLIStatusRepo(t)
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
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

func TestRepositoryScopedCommandsResolveCurrentDirectoryAndExplicitOverride(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	defaultRepo := newCLIStatusRepo(t)
	explicitRepo := newCLIStatusRepo(t)
	originalDirectory := currentWorkingDirectory
	t.Cleanup(func() { currentWorkingDirectory = originalDirectory })

	currentWorkingDirectory = func() (string, error) { return defaultRepo.Root, nil }
	defaultOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--json", "-f", workflowFile})
	})
	var defaultStatus map[string]any
	if err := json.Unmarshal([]byte(defaultOutput), &defaultStatus); err != nil {
		t.Fatal(err)
	}
	if defaultStatus["repo"] != defaultRepo.Root {
		t.Fatalf("default repository = %v, want %q", defaultStatus["repo"], defaultRepo.Root)
	}

	currentWorkingDirectory = func() (string, error) { return t.TempDir(), nil }
	explicitOutput := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "--json", "-f", workflowFile, "-C", explicitRepo.Root})
	})
	var explicitStatus map[string]any
	if err := json.Unmarshal([]byte(explicitOutput), &explicitStatus); err != nil {
		t.Fatal(err)
	}
	if explicitStatus["repo"] != explicitRepo.Root {
		t.Fatalf("explicit repository = %v, want %q", explicitStatus["repo"], explicitRepo.Root)
	}
}

func TestRepositoryDefaultRejectsNonGitBeforeWorkflowRead(t *testing.T) {
	originalDirectory := currentWorkingDirectory
	t.Cleanup(func() { currentWorkingDirectory = originalDirectory })
	nonGit := t.TempDir()
	currentWorkingDirectory = func() (string, error) { return nonGit, nil }

	err := runArgs([]string{"run", "-f", filepath.Join(nonGit, "missing.yaml")})
	if err == nil || !strings.Contains(err.Error(), "not a Git repository") || strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("non-Git default error = %v", err)
	}
}

func TestPositionalWorkflowSelectorUsesLocalAndHomeScopes(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	localPath := filepath.Join(repo.Root, ".agentflow", "workflows", "code-styling.yaml")
	globalPath := filepath.Join(home, ".agentflow", "workflows", "code-styling.yaml")
	writeCLIWorkflow(t, localPath, "local-code-styling")
	writeCLIWorkflow(t, globalPath, "global-code-styling")
	originalHome := workflowHomeDirectory
	t.Cleanup(func() { workflowHomeDirectory = originalHome })
	workflowHomeDirectory = func() (string, error) { return home, nil }

	output := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "code-styling", "--json", "-C", repo.Root})
	})
	var status map[string]any
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatal(err)
	}
	if status["workflow"] != "local-code-styling" {
		t.Fatalf("local selector result = %v", status["workflow"])
	}

	if err := os.Remove(localPath); err != nil {
		t.Fatal(err)
	}
	output = captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "code-styling", "--json", "-C", repo.Root})
	})
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatal(err)
	}
	if status["workflow"] != "global-code-styling" {
		t.Fatalf("home selector result = %v", status["workflow"])
	}
}

func TestWorkflowSelectorRejectsAmbiguousAndPathForms(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "file and positional", args: []string{"validate", "named", "-f", workflowFile}, want: "mutually exclusive"},
		{name: "multiple positional", args: []string{"validate", "one", "two"}, want: "at most one"},
		{name: "path positional", args: []string{"validate", "nested/name"}, want: "simple basename"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runArgs(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("selector error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestExplicitWorkflowFileRemainsFileOnly(t *testing.T) {
	workflowFile := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	originalDirectory := currentWorkingDirectory
	t.Cleanup(func() { currentWorkingDirectory = originalDirectory })
	currentWorkingDirectory = func() (string, error) { return t.TempDir(), nil }

	output := captureCLIStdout(t, func() error {
		return runArgs([]string{"validate", "-f", workflowFile})
	})
	if !strings.Contains(output, "valid and executable") {
		t.Fatalf("file-only validation output = %q", output)
	}
}

func TestDetachedSelectorPassesResolvedFilePath(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	workflowPath := filepath.Join(repo.Root, ".agentflow", "workflows", "code-styling.yaml")
	writeCLIWorkflow(t, workflowPath, "detached-code-styling")
	if err := os.MkdirAll(filepath.Join(home, ".agentflow", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	originalHome := workflowHomeDirectory
	originalStart := detachedStart
	t.Cleanup(func() {
		workflowHomeDirectory = originalHome
		detachedStart = originalStart
	})
	workflowHomeDirectory = func() (string, error) { return home, nil }
	var childArgs []string
	detachedStart = func(cmd *exec.Cmd) error {
		childArgs = append([]string(nil), cmd.Args[1:]...)
		cmd.Process = &os.Process{Pid: 12345}
		return nil
	}

	if err := runArgs([]string{"run", "code-styling", "--detach", "-C", repo.Root}); err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "-f", workflowPath, "-C", repo.Root, "--codex-bin", "codex"}
	if !reflect.DeepEqual(childArgs, want) {
		t.Fatalf("detached child args = %#v, want %#v", childArgs, want)
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

func writeCLIWorkflow(t *testing.T, path, name string) {
	t.Helper()
	fixture := filepath.Join("..", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	body, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), "conformance-minimal", name, 1))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
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
