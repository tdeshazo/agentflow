package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

func TestExplainUsesDurableStateForBlockedSkippedAndFailedNodes(t *testing.T) {
	t.Run("skipped", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "explain-skipped")
		w.Spec.Phases[0].If = "{{ false }}"
		e := newDurableEngine(t, w, &durableProvider{})
		if err := e.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		status, err := e.statusSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(status.TracePath); err != nil {
			t.Fatal(err)
		}
		report, err := e.Explain("change")
		if err != nil {
			t.Fatal(err)
		}
		if report.State != "skipped" || report.Source != "git-state" || report.Reason != "condition is false" {
			t.Fatalf("skipped explain = %+v", report)
		}
	})

	t.Run("failed validation", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "explain-failed")
		e := newDurableEngine(t, w, &durableProvider{action: func(_ context.Context, request provider.Request) error {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
		}})
		if err := e.Run(context.Background()); err == nil {
			t.Fatal("run unexpectedly succeeded")
		}
		report, err := e.Explain("change")
		if err != nil {
			t.Fatal(err)
		}
		if report.State != "failed" || report.Reason != "node failed deterministic validation" || report.NodeExecutionID == "" || report.Attempt != 1 {
			t.Fatalf("failed explain = %+v", report)
		}
	})

	t.Run("waiting dependencies", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := schedulingWorkflow(repo, "explain-blocked", []string{"first", "second"}, map[string][]string{"second": {"first"}}, "true")
		e := newSchedulingEngine(t, w, &schedulingProvider{})
		if err := e.initializeOrResumeState(); err != nil {
			t.Fatal(err)
		}
		report, err := e.Explain("second")
		if err != nil {
			t.Fatal(err)
		}
		if report.State != "blocked" || report.Reason != "node is waiting for declared dependencies to be deterministically accepted" || len(report.WaitingOn) != 1 || report.WaitingOn[0] != "first" {
			t.Fatalf("blocked explain = %+v", report)
		}
	})
}

func TestExplainRejectsDefinitionDriftWithoutResolvingSecrets(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "explain-drift")
	e := newDurableEngine(t, w, &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	changed := durableWorkflow(repo, "explain-drift")
	changed.Spec.Phases[0].Prompt = "changed definition"
	inspector, err := New(changed, nil, Options{RepoRoot: repo, StateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.Explain("change"); err == nil || !strings.Contains(err.Error(), "definition changed") {
		t.Fatalf("definition drift explain error = %v", err)
	}
}

func TestExplainClassifiesSerialAndParallelProviderFailures(t *testing.T) {
	t.Run("serial", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := durableWorkflow(repo, "explain-provider-serial")
		e := newDurableEngine(t, w, &durableProvider{action: func(context.Context, provider.Request) error {
			return errors.New("private provider failure")
		}})
		if err := e.Run(context.Background()); err == nil {
			t.Fatal("run unexpectedly succeeded")
		}
		report, err := e.Explain("change")
		if err != nil {
			t.Fatal(err)
		}
		if report.State != "failed" || report.FailureKind != "provider" || report.FailureStage != "provider" || report.Actor == "" || report.Provider != "durable-test" || strings.Contains(report.Reason, "private") {
			t.Fatalf("serial provider explanation = %+v", report)
		}
	})

	t.Run("parallel", func(t *testing.T) {
		repo := newDurableRepo(t)
		w := schedulingWorkflow(repo, "explain-provider-parallel", []string{"fail", "sibling"}, nil, "true")
		w.Spec.Execution.MaxParallel = 2
		w.Spec.Workspace.MutationPolicy.Allowed = []string{"fail/**", "sibling/**"}
		w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "test-model"}
		w.Spec.Phases[0].Writes = []string{"fail/**"}
		w.Spec.Phases[1].Writes = []string{"sibling/**"}
		p := &schedulingProvider{skipPhaseFile: true, action: func(_ context.Context, request provider.Request) error {
			if request.Metadata["phase"] == "fail" {
				return errors.New("parallel provider failure")
			}
			return nil
		}}
		e := newSchedulingEngine(t, w, p)
		if err := e.Run(context.Background()); err == nil {
			t.Fatal("parallel run unexpectedly succeeded")
		}
		report, err := e.Explain("fail")
		if err != nil {
			t.Fatal(err)
		}
		if report.State != "failed" || report.FailureKind != "provider" || report.Provider != "scheduler-test" {
			t.Fatalf("parallel provider explanation = %+v", report)
		}
	})
}
