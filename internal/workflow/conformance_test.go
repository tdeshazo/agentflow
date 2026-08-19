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
	normalized, err := NormalizeWorkflow(document)
	if err != nil {
		t.Fatal(err)
	}
	n := normalized.Workflow
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
	if phase := n.Spec.Phases[0]; phase.Actor != "luna" || phase.Reasoning != "high" || !phase.RequiresChange || phase.Validation != "implementation-gate" || !strings.Contains(phase.Prompt, "parameters.task") {
		t.Fatalf("implement phase = %#v", phase)
	}
	if phase := n.Spec.Phases[1]; phase.Actor != "terra" || phase.Reasoning != "high" || phase.Validation != "audit-gate" || !strings.Contains(phase.Prompt, "actual current checkout") {
		t.Fatalf("audit phase = %#v", phase)
	}
	gate := n.Spec.Validation["implementation-gate"].OnFailure
	if gate.Strategy != "repair-once" || gate.MaxRepairAttempts != 1 || gate.Repair.Actor != "terra" || gate.Repair.Reasoning != "high" {
		t.Fatalf("implementation repair policy = %#v", gate)
	}
	if audit := n.Spec.Validation["audit-gate"]; audit.OnFailure.MaxRepairAttempts != 0 || audit.Repair != "" {
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

func TestPriority4BenchmarkWorkflowsPreserveConciseSafetyContracts(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "examples", "develop-agentflow.agent-workflow.yaml"),
		filepath.Join("..", "..", "examples", "finish-priority-05.agent-workflow.yaml"),
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			document, err := Decode(path)
			if err != nil {
				t.Fatal(err)
			}
			result := Validate(document)
			if result.Status != Executable || result.Normalized == nil {
				t.Fatalf("status = %s, diagnostics = %#v", result.Status, result.Diagnostics)
			}
			w := document.Workflow
			n := result.Normalized.Workflow
			if len(w.Spec.Defaults.Phases) == 0 || w.Spec.Defaults.Agent.Runner != "codex" {
				t.Fatalf("workflow does not use authoring defaults: %#v", w.Spec.Defaults)
			}
			if len(w.Spec.Recovery.ActivePhase) != 0 || w.Spec.Temp.Directory != "" {
				t.Fatalf("workflow retains procedural/runtime plumbing: recovery=%#v temp=%#v", w.Spec.Recovery, w.Spec.Temp)
			}
			if w.Spec.Tools["canonical-gate"].Capture.Log != "" {
				t.Fatal("canonical gate still authors interpreter log plumbing")
			}
			if _, ok := w.Spec.Tools["assert-change-scope"]; ok {
				t.Fatal("scope assertion is duplicated as authored validation plumbing")
			}
			if n.Spec.Lifecycle.Policy != "safe-resume" {
				t.Fatalf("normalized lifecycle = %#v", n.Spec.Lifecycle)
			}
			for name, agent := range n.Spec.Agents {
				if agent.Runner != "codex" || agent.Sandbox != "workspace-write" || agent.Approval != "never" || !agent.Ephemeral || agent.Color != "never" || !agent.MayCommit || !agent.OutputLastMessage {
					t.Fatalf("agent %q lost inherited execution authority: %#v", name, agent)
				}
			}
			if len(n.Spec.Workspace.MutationPolicy.Allowed) == 0 || len(n.Spec.Workspace.MutationPolicy.Integrity) == 0 || n.Spec.Workspace.Cleanliness.BeforeFirstRun != "required" {
				t.Fatal("normalized safety policy lost scope, integrity, or cleanliness")
			}
			for _, phase := range w.Spec.Phases {
				if phase.Kind == "criterion" {
					prompt := strings.ToLower(phase.Prompt)
					if phase.CriterionID == "" || phase.Criterion != "" || !phase.AdvanceProgress || (strings.Contains(prompt, "mark") && strings.Contains(prompt, "acceptance")) {
						t.Fatalf("criterion phase is not engine-accepted: %#v", phase)
					}
				}
			}
			plan, err := BuildExpandedPlan(document)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.SafetyEnforcementPoints) < 4 || len(plan.RecoveryBehavior) < 4 || len(plan.Validations) == 0 || len(plan.CompletionContract) == 0 {
				t.Fatalf("expanded plan hides runtime contract: %#v", plan)
			}
			if filepath.Base(path) == "finish-priority-05.agent-workflow.yaml" {
				bookkeeping := n.Spec.Phases[len(n.Spec.Phases)-1]
				if bookkeeping.Kind != "bookkeeping" || bookkeeping.Actor != "" || len(bookkeeping.Bookkeeping) != 2 {
					t.Fatalf("bookkeeping phase = %#v", bookkeeping)
				}
				if bookkeeping.Bookkeeping[0].Type != "markdown-status" || bookkeeping.Bookkeeping[1].Type != "markdown-index" || bookkeeping.Bookkeeping[1].State != "checked" {
					t.Fatalf("bookkeeping transitions = %#v", bookkeeping.Bookkeeping)
				}
				if len(plan.ProgressTransitions) != 6 || plan.Phases[len(plan.Phases)-1].Validation != "phaseGate" {
					t.Fatalf("priority 5 progress/phase plan = %#v", plan)
				}
				acceptance := plan.Phases[len(plan.Phases)-1].Acceptance
				if strings.Contains(strings.Join(acceptance, " "), "run actor") || !strings.Contains(strings.Join(acceptance, " "), "deterministic bookkeeping") {
					t.Fatalf("expanded bookkeeping authority = %#v", acceptance)
				}
			}
		})
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
		"go run . validate -f examples/develop-agentflow.agent-workflow.yaml",
	} {
		if strings.Contains(string(ci), duplicate) {
			t.Fatalf("CI repeats canonical-gate-owned check %q", duplicate)
		}
	}
}
