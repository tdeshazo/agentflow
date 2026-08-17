package workflow

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestConformanceCorpus(t *testing.T) {
	cases := []struct {
		name   string
		status Status
	}{
		{name: "valid/minimal.yaml", status: Executable},
		{name: "valid/concise-defaults.yaml", status: Executable},
		{name: "unsupported/runtime-surface.yaml", status: Unsupported},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(filepath.Join("testdata", "conformance", tc.name))
			if result.Status != tc.status {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, tc.status, result.Diagnostics)
			}
		})
	}
}

func TestConformanceInvalidDiagnostics(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		status   Status
		contains string
	}{
		{name: "unknown-executable-field.yaml", status: Invalid, contains: "field unknowable not found"},
		{name: "malformed-type.yaml", status: Invalid, contains: "cannot unmarshal"},
		{name: "duplicate-identifiers.yaml", path: "spec.preconditions[1].id", status: Invalid, contains: "duplicate check id"},
		{name: "unknown-references.yaml", path: "spec.phases[0].actor", status: Invalid, contains: "unknown agent"},
		{name: "invalid-expression.yaml", path: "spec.workspace.root", status: Invalid, contains: "unknown parameter reference"},
		{name: "malformed-expression.yaml", path: "spec.state.reset.when", status: Invalid, contains: "missing closing delimiter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := ValidateFile(filepath.Join("testdata", "conformance", "invalid", tc.name))
			if result.Status != tc.status {
				t.Fatalf("status = %s, want %s; diagnostics = %#v", result.Status, tc.status, result.Diagnostics)
			}
			for _, diagnostic := range result.Diagnostics {
				if (tc.path == "" || diagnostic.Path == tc.path) &&
					diagnostic.Status == tc.status && strings.Contains(diagnostic.Message, tc.contains) {
					return
				}
			}
			t.Fatalf("no diagnostic with path %q and message containing %q: %#v", tc.path, tc.contains, result.Diagnostics)
		})
	}
}

func TestConformanceDiagnosticOrderIsStable(t *testing.T) {
	path := filepath.Join("testdata", "conformance", "invalid", "unknown-references.yaml")
	first := ValidateFile(path)
	for i := 0; i < 20; i++ {
		result := ValidateFile(path)
		if len(result.Diagnostics) != len(first.Diagnostics) {
			t.Fatalf("run %d diagnostics = %d, want %d", i, len(result.Diagnostics), len(first.Diagnostics))
		}
		for j, diagnostic := range result.Diagnostics {
			want := first.Diagnostics[j]
			if diagnostic.Status != want.Status || diagnostic.Path != want.Path || diagnostic.Position != want.Position || diagnostic.Message != want.Message {
				t.Fatalf("run %d diagnostic %d changed:\n got %#v\nwant %#v", i, j, diagnostic, want)
			}
		}
	}
}

func TestConformanceShippedDefinitions(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "spec", "agent-workflow-v1alpha1.yaml"),
		filepath.Join("..", "..", "examples", "finish-priority-05.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "develop-agentflow.agent-workflow.yaml"),
	}
	for _, path := range paths {
		result := ValidateFile(path)
		if result.Status == Invalid {
			t.Fatalf("%s invalid: %#v", path, result.Diagnostics)
		}
	}
}

func TestDevelopAgentFlowWorkflowContract(t *testing.T) {
	path := filepath.Join("..", "..", "examples", "develop-agentflow.agent-workflow.yaml")
	document, err := Decode(path)
	if err != nil {
		t.Fatal(err)
	}
	if result := Validate(document); result.Status != Executable {
		t.Fatalf("self-hosting workflow status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
	}
	w := document.Workflow
	if w.Metadata.Name != "develop-agentflow" {
		t.Fatalf("metadata.name = %q", w.Metadata.Name)
	}
	if task, ok := w.Spec.Parameters["task"]; !ok || task.Type != "string" {
		t.Fatalf("task parameter = %#v, want string parameter", task)
	}
	boundedTaskCheck := false
	for _, step := range w.Spec.Flow {
		if step.ID == "require-bounded-task" && step.If == "{{ parameters.task == '' }}" && len(step.Then) == 1 && step.Then[0].Stop != "" {
			boundedTaskCheck = true
		}
	}
	if !boundedTaskCheck {
		t.Fatal("self-hosting workflow must block empty tasks before provider execution")
	}
	byID := map[string]Phase{}
	for _, phase := range w.Spec.Phases {
		byID[phase.ID] = phase
	}
	if phase := byID["implement"]; phase.Actor != "luna" || phase.Reasoning != "high" || !phase.RequiresChange || !strings.Contains(phase.Prompt, "parameters.task") {
		t.Fatalf("implement phase = %#v", phase)
	}
	if phase := byID["audit"]; phase.Actor != "terra" || phase.Reasoning != "high" || !strings.Contains(phase.Prompt, "actual current checkout") {
		t.Fatalf("audit phase = %#v", phase)
	}
	gate := w.Spec.Validation["implementation-gate"].OnFailure
	if gate.Strategy != "repair-once" || gate.MaxRepairAttempts != 1 || gate.Repair.Actor != "terra" || gate.Repair.Reasoning != "high" {
		t.Fatalf("implementation repair policy = %#v", gate)
	}
	if audit := w.Spec.Validation["audit-gate"]; audit.OnFailure.MaxRepairAttempts != 0 || audit.Repair != "none" {
		t.Fatalf("audit gate must not add a repair attempt: %#v", audit)
	}
	var review *HumanGate
	for i := range w.Spec.HumanGates {
		if w.Spec.HumanGates[i].ID == "self-host-review" {
			review = &w.Spec.HumanGates[i]
		}
	}
	if review == nil || review.Acknowledgement.Type != "exact-text" || review.Acknowledgement.Value != "yes" {
		t.Fatalf("self-host-review gate = %#v", review)
	}
	protected := map[string]bool{}
	for _, rule := range w.Spec.Workspace.MutationPolicy.Integrity {
		for _, path := range rule.Paths {
			protected[path] = true
		}
	}
	for _, path := range []string{"ROADMAP.md", "docs/research/**", "scripts/check.sh", "examples/develop-agentflow.agent-workflow.yaml", ".github/workflows/quality.yml"} {
		if !protected[path] {
			t.Errorf("protected integrity path %q is missing", path)
		}
	}
}

func TestSelfHostingCutoverContract(t *testing.T) {
	root := filepath.Join("..", "..")
	bootstrap, err := os.ReadFile(filepath.Join(root, "bootstrap-agentflow-mvp.sh"))
	if err != nil {
		t.Fatal(err)
	}

	for _, forbidden := range []string{
		"require_human_verification",
		"refs/agentflow/develop-agentflow",
		"phases/implement",
		"phases/audit",
		"human/self-host-review",
		"sh scripts/check.sh",
	} {
		if strings.Contains(string(bootstrap), forbidden) {
			t.Fatalf("bootstrap contains private or obsolete cutover contract %q", forbidden)
		}
	}

	document, err := Decode(filepath.Join(root, "examples", "develop-agentflow.agent-workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	workflow := document.Workflow
	setPattern := regexp.MustCompile(`--set "([A-Za-z_][A-Za-z0-9_]*)=`)
	for _, match := range setPattern.FindAllStringSubmatch(string(bootstrap), -1) {
		if _, ok := workflow.Spec.Parameters[match[1]]; !ok {
			t.Fatalf("bootstrap passes undeclared parameter %q", match[1])
		}
	}
	if !strings.Contains(string(bootstrap), "./scripts/check.sh") {
		t.Fatal("bootstrap does not invoke the canonical Bash gate directly")
	}
	if got, want := workflow.Spec.Tools["canonical-gate"].Command, "./{{ spec.paths.canonical_gate }}"; got != want {
		t.Fatalf("canonical-gate command = %q, want %q", got, want)
	}
	commands := map[string]bool{}
	for _, check := range workflow.Spec.Preconditions {
		if check.Type == "commands-exist" {
			for _, command := range check.Commands {
				commands[command] = true
			}
		}
	}
	for _, command := range []string{"git", "codex", "sh", "bash", "go", "gofmt", "rg"} {
		if !commands[command] {
			t.Errorf("required-commands is missing %q", command)
		}
	}

	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "quality.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ci), "run: ./scripts/check.sh") {
		t.Fatal("CI does not delegate to the canonical gate")
	}
	for _, duplicate := range []string{
		"go test ./internal/engine -run '^TestSelfHosting'",
		"go run ./cmd/agentflow validate -f examples/develop-agentflow.agent-workflow.yaml",
	} {
		if strings.Contains(string(ci), duplicate) {
			t.Fatalf("CI repeats canonical-gate-owned check %q", duplicate)
		}
	}
}
