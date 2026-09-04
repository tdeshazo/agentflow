package agentflowcli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadCLIConfigMergesGlobalAndLocalFields(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	writeCLIConfig(t, home, `
codex_bin = "/global/codex"

[parameters]
shared = "global"
global_only = "yes"

[run]
workflow = "global-run"
detach = true

[status]
all = true

[logs]
follow = true
`)
	writeCLIConfig(t, root, `
[parameters]
shared = "local"
local_only = "yes"

[run]
workflow = "local-run"

[status]
workflow = "local-status"
detail = true

[logs]
tail = 25
`)

	config, err := loadCLIConfig(root, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got := configuredString(config.CodexBin, ""); got != "/global/codex" {
		t.Fatalf("codex_bin = %q", got)
	}
	if got, want := config.Parameters, map[string]string{"shared": "local", "global_only": "yes", "local_only": "yes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parameters = %#v, want %#v", got, want)
	}
	if got := configuredString(config.Run.Workflow, ""); got != "local-run" || !configuredBool(config.Run.Detach, false) {
		t.Fatalf("run defaults = %#v", config.Run)
	}
	if got := configuredString(config.Status.Workflow, ""); got != "local-status" || config.Status.All != nil || !configuredBool(config.Status.Detail, false) {
		t.Fatalf("status defaults = %#v", config.Status)
	}
	if got := configuredInt(config.Logs.Tail, -1); got != 25 || config.Logs.Follow != nil {
		t.Fatalf("logs defaults = %#v", config.Logs)
	}
}

func TestLoadCLIConfigRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{name: "unknown field", config: `unknown = true`, want: "strict mode"},
		{name: "wrong type", config: `codex_bin = 42`, want: "cannot decode TOML integer"},
		{name: "empty parameter", config: "[parameters]\n\"\" = \"value\"\n", want: "empty key"},
		{name: "path selector", config: "[run]\nworkflow = \"nested/workflow\"\n", want: "run.workflow"},
		{name: "status conflict", config: "[status]\nworkflow = \"one\"\nall = true\n", want: "mutually exclusive"},
		{name: "status detail conflict", config: "[status]\nall = true\ndetail = true\n", want: "mutually exclusive"},
		{name: "logs conflict", config: "[logs]\ntail = 10\nfollow = true\n", want: "mutually exclusive"},
		{name: "negative tail", config: "[logs]\ntail = -1\n", want: "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			path := writeCLIConfig(t, root, tt.config)
			_, err := loadCLIConfig(root, func() (string, error) { return home, nil })
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("config error = %v, want path and %q", err, tt.want)
			}
		})
	}
}

func TestCLIUsesConfiguredWorkflowAndExplicitSelectorWins(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	configuredPath := filepath.Join(repo.Root, ".agentflow", "workflows", "configured.yaml")
	explicitPath := filepath.Join(repo.Root, ".agentflow", "workflows", "explicit.yaml")
	activePath := filepath.Join(repo.Root, ".agentflow", "workflows", "active.yaml")
	writeCLIWorkflow(t, configuredPath, "configured-workflow")
	writeCLIWorkflow(t, explicitPath, "explicit-workflow")
	writeCLIWorkflow(t, activePath, "active-workflow")
	writeCLIConfig(t, repo.Root, "[status]\nworkflow = \"configured\"\njson = true\ndetail = true\n")
	store := newSelectionStore(repo)
	if err := store.Select("active"); err != nil {
		t.Fatal(err)
	}

	originalHome := workflowHomeDirectory
	t.Cleanup(func() { workflowHomeDirectory = originalHome })
	workflowHomeDirectory = func() (string, error) { return home, nil }

	assertWorkflow := func(args []string, want string) {
		t.Helper()
		output := captureCLIStdout(t, func() error { return runArgs(args) })
		var status map[string]any
		if err := json.Unmarshal([]byte(output), &status); err != nil {
			t.Fatalf("status output = %q: %v", output, err)
		}
		if status["workflow"] != want {
			t.Fatalf("workflow = %v, want %q", status["workflow"], want)
		}
		if _, ok := status["detail"].(map[string]any); !ok {
			t.Fatalf("configured status detail = %#v", status["detail"])
		}
	}
	assertWorkflow([]string{"status", "-C", repo.Root}, "configured-workflow")
	assertWorkflow([]string{"status", "explicit", "-C", repo.Root}, "explicit-workflow")
	assertWorkflow([]string{"status", "-f", explicitPath, "-C", repo.Root}, "explicit-workflow")
	selection, found, err := store.Read()
	if err != nil || !found || selection.Current != "active" {
		t.Fatalf("active selection after explicit selectors = %+v, found %v, err %v", selection, found, err)
	}
}

func TestExplicitSelectorOverridesConfiguredStatusAll(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	workflowPath := filepath.Join(repo.Root, ".agentflow", "workflows", "explicit.yaml")
	writeCLIWorkflow(t, workflowPath, "explicit-workflow")
	writeCLIConfig(t, repo.Root, "[status]\nall = true\njson = true\n")

	originalHome := workflowHomeDirectory
	t.Cleanup(func() { workflowHomeDirectory = originalHome })
	workflowHomeDirectory = func() (string, error) { return home, nil }

	output := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "explicit", "-C", repo.Root})
	})
	var status map[string]any
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		t.Fatalf("status output = %q: %v", output, err)
	}
	if status["workflow"] != "explicit-workflow" {
		t.Fatalf("workflow = %v", status["workflow"])
	}
}

func TestConfiguredStatusAllIgnoresActiveSelection(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	writeCLIWorkflow(t, filepath.Join(repo.Root, ".agentflow", "workflows", "active.yaml"), "active-workflow")
	writeCLIConfig(t, repo.Root, "[status]\nall = true\njson = true\n")
	if err := newSelectionStore(repo).Select("active"); err != nil {
		t.Fatal(err)
	}

	originalHome := workflowHomeDirectory
	t.Cleanup(func() { workflowHomeDirectory = originalHome })
	workflowHomeDirectory = func() (string, error) { return home, nil }

	output := captureCLIStdout(t, func() error {
		return runArgs([]string{"status", "-C", repo.Root})
	})
	if !strings.Contains(output, `"workflows":[]`) || strings.Contains(output, `"workflow":"active-workflow"`) {
		t.Fatalf("configured status --all output = %q", output)
	}
}

func TestCLIConfigRootFollowsExplicitC(t *testing.T) {
	target := newCLIStatusRepo(t)
	other := t.TempDir()
	home := t.TempDir()
	workflowPath := filepath.Join(target.Root, ".agentflow", "workflows", "target.yaml")
	writeCLIWorkflow(t, workflowPath, "target-workflow")
	writeCLIConfig(t, target.Root, "[status]\nworkflow = \"target\"\njson = true\n")

	originalDirectory := currentWorkingDirectory
	originalHome := workflowHomeDirectory
	t.Cleanup(func() {
		currentWorkingDirectory = originalDirectory
		workflowHomeDirectory = originalHome
	})
	currentWorkingDirectory = func() (string, error) { return other, nil }
	workflowHomeDirectory = func() (string, error) { return home, nil }

	output := captureCLIStdout(t, func() error { return runArgs([]string{"status", "-C", target.Root}) })
	if !strings.Contains(output, `"workflow":"target-workflow"`) {
		t.Fatalf("status output = %q", output)
	}
}

func TestCommandLineRepoSkipsOtherFlagValuesAndStopsAtDoubleDash(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"--set", "task=-C", "-C", "/target"}, want: "/target"},
		{args: []string{"-f", "-C", "--", "-C", "/ignored"}, want: ""},
		{args: []string{"--C=/target"}, want: "/target"},
	}
	for _, tt := range tests {
		if got := commandLineRepo(tt.args); got != tt.want {
			t.Fatalf("commandLineRepo(%#v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestConfiguredDetachedRunMaterializesEffectiveArguments(t *testing.T) {
	repo := newCLIStatusRepo(t)
	home := t.TempDir()
	workflowPath := filepath.Join(repo.Root, ".agentflow", "workflows", "configured.yaml")
	writeCLIWorkflow(t, workflowPath, "configured-detached")
	writeCLIConfig(t, repo.Root, `
codex_bin = "/configured/codex"

[parameters]
alpha = "one"
zeta = "two"

[run]
workflow = "configured"
detach = true
`)

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
		writeDetachedTestReady(t, cmd, true)
		return nil
	}

	if err := runArgs([]string{"run", "-C", repo.Root}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"run", "-f", workflowPath, "-C", repo.Root,
		"--codex-bin", "/configured/codex",
		"--set", "alpha=one", "--set", "zeta=two",
	}
	if !reflect.DeepEqual(childArgs, want) {
		t.Fatalf("detached child args = %#v, want %#v", childArgs, want)
	}
}

func writeCLIConfig(t *testing.T, root, contents string) string {
	t.Helper()
	path := filepath.Join(root, ".agentflow", configFileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
