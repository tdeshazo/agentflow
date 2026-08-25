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

var errAdversarialInterruption = errors.New("deterministic simulated interruption")

const interruptionBeforeProviderReturn interruptionPoint = "before-provider-return"

type adversarialPanic struct {
	point interruptionPoint
}

func (p adversarialPanic) Error() string {
	return "simulated process interruption at " + string(p.point)
}

func TestAdversarialPendingRepairAttributionAcrossCrashWindows(t *testing.T) {
	tests := []struct {
		name             string
		primaryMayCommit bool
		repairMayCommit  bool
		point            interruptionPoint
		panicInProvider  bool
	}{
		{name: "primary-authorized repair-unauthorized after provider return", primaryMayCommit: true, point: interruptionAfterProviderReturn},
		{name: "primary-unauthorized repair-authorized after provider return", repairMayCommit: true, point: interruptionAfterProviderReturn},
		{name: "primary-authorized repair-unauthorized after authority", primaryMayCommit: true, point: interruptionAfterAuthority},
		{name: "primary-unauthorized repair-authorized after authority", repairMayCommit: true, point: interruptionAfterAuthority},
		{name: "primary-authorized repair-unauthorized before provider return", primaryMayCommit: true, panicInProvider: true},
		{name: "primary-unauthorized repair-authorized before provider return", repairMayCommit: true, panicInProvider: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			counter := filepath.Join(t.TempDir(), "phase-validation-count")
			w := durableWorkflow(repo, "adversarial-repair-"+strings.ReplaceAll(tt.name, " ", "-"))
			w.Spec.Parameters["counter"] = workflow.Parameter{Type: "path", Default: counter}
			w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf x >> {{ parameters.counter }}; grep -qx complete work.txt"}
			worker := w.Spec.Agents["worker"]
			worker.MayCommit = tt.primaryMayCommit
			w.Spec.Agents["worker"] = worker
			w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: tt.repairMayCommit}
			w.Spec.Validation["phaseGate"] = repairValidation()

			p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
				path := filepath.Join(request.Workspace, "work.txt")
				if request.Metadata["actor"] != "repair" {
					if err := os.WriteFile(path, []byte("partial\n"), 0o644); err != nil {
						return err
					}
					if tt.primaryMayCommit {
						gitIn(t, request.Workspace, "add", "work.txt")
						gitIn(t, request.Workspace, "commit", "-qm", "primary partial commit")
					}
					return nil
				}
				if err := os.WriteFile(path, []byte("complete\n"), 0o644); err != nil {
					return err
				}
				gitIn(t, request.Workspace, "add", "work.txt")
				gitIn(t, request.Workspace, "commit", "-qm", "repair commit")
				if tt.panicInProvider {
					panic(adversarialPanic{point: interruptionBeforeProviderReturn})
				}
				return nil
			}}
			e := newDurableEngine(t, w, p)
			if !tt.panicInProvider {
				e.interruptionHook = func(point interruptionPoint, pending PendingActorInvocation) error {
					if point == tt.point && pending.Actor == "repair" {
						return errAdversarialInterruption
					}
					return nil
				}
			}

			if tt.panicInProvider {
				assertProviderPanicInterruption(t, e)
			} else if err := e.Run(context.Background()); !errors.Is(err, errAdversarialInterruption) {
				t.Fatalf("interrupted run error = %v, want simulated interruption", err)
			}
			assertPendingInvocationExists(t, e)
			var active ActivePhase
			if ok, err := e.Store.GetJSON(e.activeRecord(), &active); err != nil || !ok || active.RepairAttempts["phaseGate"] != 1 {
				t.Fatalf("durable repair budget = %+v ok=%v err=%v", active, ok, err)
			}

			restarted := newDurableEngine(t, w, p)
			err := restarted.Run(context.Background())
			if !tt.repairMayCommit {
				var safetyErr *safetyViolation
				if !errors.As(err, &safetyErr) || safetyErr.actor != "repair" {
					t.Fatalf("restart error = %v, want repair safety failure", err)
				}
				if p.calls != 2 {
					t.Fatalf("safety restart provider calls = %d, want primary plus one repair", p.calls)
				}
				if ok, readErr := restarted.Store.GetJSON(restarted.activeRecord(), &active); readErr != nil || !ok || active.FailureKind != PhaseFailureSafety || active.CommitActor != "repair" {
					t.Fatalf("durable repair safety = %+v ok=%v err=%v", active, ok, readErr)
				}
				assertNoDurablePhaseOrCompletionMarkers(t, restarted, "change")
				if got := adversarialFileLength(t, counter); got != 1 {
					t.Fatalf("unsafe restart deterministic validation count = %d, want initial validation only", got)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if p.calls != 2 {
					t.Fatalf("authorized restart provider calls = %d, want primary plus one repair", p.calls)
				}
				assertDurableCompletion(t, restarted, repo)
				if got := adversarialFileLength(t, counter); got != 2 {
					t.Fatalf("authorized restart deterministic validation count = %d, want initial plus revalidation", got)
				}
			}
			if _, ok, err := restarted.Store.Resolve(restarted.pendingInvocationRecord()); err != nil || ok {
				t.Fatalf("pending invocation survived deterministic restart: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestAdversarialPendingWriteInterruptionRunsNoProviderBeforeRestart(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "adversarial-pending-write")
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", MayCommit: true}
	w.Spec.Validation["phaseGate"] = repairValidation()
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if request.Metadata["actor"] == "repair" {
			return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644)
		}
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("partial\n"), 0o644)
	}}
	e := newDurableEngine(t, w, p)
	e.interruptionHook = func(point interruptionPoint, pending PendingActorInvocation) error {
		if point == interruptionAfterPendingInvocation && pending.Actor == "worker" {
			return errAdversarialInterruption
		}
		return nil
	}
	if err := e.Run(context.Background()); !errors.Is(err, errAdversarialInterruption) {
		t.Fatalf("pending-write interruption error = %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider ran before pending-write interruption: calls=%d", p.calls)
	}
	assertPendingInvocationExists(t, e)

	if err := newDurableEngine(t, w, p).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.calls != 2 {
		t.Fatalf("restart calls = %d, want primary plus one repair", p.calls)
	}
}

func TestAdversarialCompletionRepairAttributionAcrossCrashWindows(t *testing.T) {
	for _, tt := range []struct {
		name            string
		repairMayCommit bool
		point           interruptionPoint
	}{
		{name: "unauthorized repair after provider return", point: interruptionAfterProviderReturn},
		{name: "authorized repair after provider return", repairMayCommit: true, point: interruptionAfterProviderReturn},
		{name: "unauthorized repair after authority", point: interruptionAfterAuthority},
		{name: "authorized repair after authority", repairMayCommit: true, point: interruptionAfterAuthority},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			counter := filepath.Join(t.TempDir(), "completion-validation-count")
			w := completionRepairWorkflow(repo, "adversarial-completion-"+strings.ReplaceAll(tt.name, " ", "-"))
			w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", Model: "worker-model", MayCommit: false}
			w.Spec.Agents["repair"] = workflow.Agent{Runner: "test", Model: "repair-model", MayCommit: tt.repairMayCommit}
			w.Spec.Parameters = map[string]workflow.Parameter{"counter": {Type: "path", Default: counter}}
			w.Spec.Tools["final"] = workflow.Tool{Type: "shell", Command: "printf x >> {{ parameters.counter }}; test -f completion.txt"}
			p := &schedulingProvider{action: func(_ context.Context, request provider.Request) error {
				if request.Metadata["actor"] != "repair" {
					return nil
				}
				if err := os.WriteFile(filepath.Join(request.Workspace, "completion.txt"), []byte("done\n"), 0o644); err != nil {
					return err
				}
				gitIn(t, request.Workspace, "add", "completion.txt")
				gitIn(t, request.Workspace, "commit", "-qm", "completion repair commit")
				return nil
			}}
			e := newSchedulingEngine(t, w, p)
			e.interruptionHook = func(point interruptionPoint, pending PendingActorInvocation) error {
				if point == tt.point && pending.Actor == "repair" {
					return errAdversarialInterruption
				}
				return nil
			}
			if err := e.Run(context.Background()); !errors.Is(err, errAdversarialInterruption) {
				t.Fatalf("interrupted completion run error = %v", err)
			}
			if got := len(mustReadFile(t, counter)); got != 1 {
				t.Fatalf("validation executions before restart = %d, want 1", got)
			}
			assertPendingInvocationExists(t, e)

			restarted := newSchedulingEngine(t, w, p)
			err := restarted.Run(context.Background())
			if !tt.repairMayCommit {
				var safetyErr *safetyViolation
				if !errors.As(err, &safetyErr) || safetyErr.actor != "repair" {
					t.Fatalf("completion restart error = %v, want repair safety failure", err)
				}
				if len(p.calls) != 2 || len(mustReadFile(t, counter)) != 1 {
					t.Fatalf("unsafe completion restart did more work: calls=%d validations=%d", len(p.calls), len(mustReadFile(t, counter)))
				}
				assertNoSchedulingCompletion(t, restarted)
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if got := len(mustReadFile(t, counter)); got != 2 {
					t.Fatalf("final validation executions = %d, want one post-restart revalidation", got)
				}
				assertSchedulingCompletion(t, restarted)
			}
			if _, ok, err := restarted.Store.Resolve(restarted.pendingInvocationRecord()); err != nil || ok {
				t.Fatalf("completion pending invocation survived restart: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestAdversarialPendingStateIsPrivateAndIdentityBound(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "pending-state-privacy")
	w.Spec.Parameters["task"] = workflow.Parameter{Type: "string", Default: ""}
	w.Spec.Phases[0].Prompt = "super-secret prompt"
	worker := w.Spec.Agents["worker"]
	worker.MayCommit = false
	w.Spec.Agents["worker"] = worker
	initialProvider := &durableProvider{}
	e, err := New(w, map[string]provider.Provider{"test": initialProvider}, Options{Overrides: map[string]string{"task": "old executable task"}})
	if err != nil {
		t.Fatal(err)
	}
	e.interruptionHook = func(point interruptionPoint, pending PendingActorInvocation) error {
		if point == interruptionAfterPendingInvocation && pending.Actor == "worker" {
			return errAdversarialInterruption
		}
		return nil
	}
	if err := e.Run(context.Background()); !errors.Is(err, errAdversarialInterruption) {
		t.Fatalf("privacy interruption error = %v", err)
	}
	if initialProvider.calls != 0 {
		t.Fatalf("privacy interruption invoked provider %d times", initialProvider.calls)
	}
	sha, ok, err := e.Store.Resolve(e.pendingInvocationRecord())
	if err != nil || !ok {
		t.Fatalf("pending record: sha=%q ok=%v err=%v", sha, ok, err)
	}
	blob := gitIn(t, repo, "cat-file", "blob", sha)
	if strings.Contains(blob, "old executable task") || strings.Contains(blob, "super-secret prompt") {
		t.Fatalf("pending invocation persisted secret or prompt material: %s", blob)
	}

	other := durableWorkflow(repo, "pending-state-privacy-other")
	otherEngine := newDurableEngine(t, other, &durableProvider{})
	if otherEngine.Store.Namespace == e.Store.Namespace {
		t.Fatal("workflow namespaces unexpectedly overlap")
	}
	if _, ok, err := otherEngine.Store.Resolve(otherEngine.pendingInvocationRecord()); err != nil || ok {
		t.Fatalf("pending state crossed workflow namespace: ok=%v err=%v", ok, err)
	}

	p := &durableProvider{}
	restarted, err := New(w, map[string]provider.Provider{"test": p}, Options{Overrides: map[string]string{"task": "new executable task"}})
	if err != nil {
		t.Fatal(err)
	}
	err = restarted.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "resolved run inputs changed") {
		t.Fatalf("changed executable input reused pending state: %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("identity mismatch invoked provider %d times", p.calls)
	}
	if _, ok, err := restarted.Store.Resolve(restarted.pendingInvocationRecord()); err != nil || !ok {
		t.Fatalf("identity mismatch unexpectedly consumed pending state: ok=%v err=%v", ok, err)
	}
}

func TestAdversarialTerminalPhaseSafetySurvivesHeadChangeAndExplicitReset(t *testing.T) {
	repo := newDurableRepo(t)
	counter := filepath.Join(t.TempDir(), "safety-validation-count")
	w := durableWorkflow(repo, "adversarial-terminal-reset")
	w.Spec.Parameters["counter"] = workflow.Parameter{Type: "path", Default: counter}
	w.Spec.Tools["gate"] = workflow.Tool{Type: "shell", Command: "printf x >> {{ parameters.counter }}; grep -q complete work.txt"}
	w.Spec.Agents["worker"] = workflow.Agent{Runner: "test", MayCommit: false}
	p := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		if err := os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete\n"), 0o644); err != nil {
			return err
		}
		gitIn(t, request.Workspace, "add", "work.txt")
		gitIn(t, request.Workspace, "commit", "-qm", "unauthorized actor commit")
		return nil
	}}
	e := newDurableEngine(t, w, p)
	var safetyErr *safetyViolation
	if err := e.Run(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("initial actor safety error = %v", err)
	}
	if p.calls != 1 || adversarialFileLength(t, counter) != 0 {
		t.Fatalf("initial safety work = calls=%d validations=%d", p.calls, adversarialFileLength(t, counter))
	}

	if err := os.WriteFile(filepath.Join(repo, "manual.txt"), []byte("manual\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "manual.txt")
	gitIn(t, repo, "commit", "-qm", "manual head change")
	restarted := newDurableEngine(t, w, p)
	if err := restarted.Run(context.Background()); !errors.As(err, &safetyErr) {
		t.Fatalf("post-head-change error = %v, want terminal safety", err)
	}
	if p.calls != 1 || adversarialFileLength(t, counter) != 0 {
		t.Fatalf("terminal safety was bypassed: calls=%d validations=%d", p.calls, adversarialFileLength(t, counter))
	}

	if err := restarted.Reset(); err != nil {
		t.Fatalf("explicit reset after terminal safety: %v", err)
	}
	if _, ok, err := restarted.Store.Resolve(restarted.activeRecord()); err != nil || ok {
		t.Fatalf("reset left terminal active state: ok=%v err=%v", ok, err)
	}
	repairedProvider := &durableProvider{action: func(_ context.Context, request provider.Request) error {
		return os.WriteFile(filepath.Join(request.Workspace, "work.txt"), []byte("complete after reset\n"), 0o644)
	}}
	if err := newDurableEngine(t, w, repairedProvider).Run(context.Background()); err != nil {
		t.Fatalf("new run after explicit reset: %v", err)
	}
}

func assertProviderPanicInterruption(t *testing.T, e *Engine) {
	t.Helper()
	var interrupted any
	func() {
		defer func() { interrupted = recover() }()
		_ = e.Run(context.Background())
	}()
	if interrupted == nil {
		t.Fatal("provider interruption hook returned normally")
	}
	if _, ok := interrupted.(adversarialPanic); !ok {
		t.Fatalf("panic = %v, want adversarial interruption", interrupted)
	}
}

func adversarialFileLength(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

func assertPendingInvocationExists(t *testing.T, e *Engine) {
	t.Helper()
	if _, ok, err := e.Store.Resolve(e.pendingInvocationRecord()); err != nil || !ok {
		t.Fatalf("pending invocation missing after interruption: ok=%v err=%v", ok, err)
	}
}
