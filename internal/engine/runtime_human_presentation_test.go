package engine

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestInteractiveHumanGateConfirmsChecklistBeforeFinalAcknowledgement(t *testing.T) {
	original := humanGateInteractive
	humanGateInteractive = func(io.Reader, io.Writer) bool { return true }
	t.Cleanup(func() { humanGateInteractive = original })

	repo := newSelfHostingRepo(t)
	w := selfHostingWorkflow(t, repo)
	p := &selfHostingFakeProvider{commitLuna: true}
	e := newSelfHostingEngine(t, w, p)
	e.In = strings.NewReader("y\ny\ny\nyes\n")
	var out bytes.Buffer
	e.Out = &out

	if err := e.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"1. ", "[y/N]:", "All checklist items confirmed.", `Type "yes" to confirm:`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("interactive human gate output missing %q:\n%s", want, out.String())
		}
	}
	if _, ok, err := e.Store.Resolve("human/self-host-review"); err != nil || !ok {
		t.Fatalf("human evidence: ok=%v err=%v", ok, err)
	}
}

func TestInteractiveHumanGateRejectsUncheckedChecklistItemWithoutEvidence(t *testing.T) {
	original := humanGateInteractive
	humanGateInteractive = func(io.Reader, io.Writer) bool { return true }
	t.Cleanup(func() { humanGateInteractive = original })

	repo := newSelfHostingRepo(t)
	w := selfHostingWorkflow(t, repo)
	e := newSelfHostingEngine(t, w, &selfHostingFakeProvider{commitLuna: true})
	e.In = strings.NewReader("n\n")
	var out bytes.Buffer
	e.Out = &out

	err := e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checklist item 1 not confirmed") {
		t.Fatalf("human gate error = %v", err)
	}
	if _, ok, resolveErr := e.Store.Resolve("human/self-host-review"); resolveErr != nil || ok {
		t.Fatalf("rejected checklist wrote human evidence: ok=%v err=%v", ok, resolveErr)
	}
}

func TestInteractiveHumanGateStillRequiresExactFinalAcknowledgement(t *testing.T) {
	original := humanGateInteractive
	humanGateInteractive = func(io.Reader, io.Writer) bool { return true }
	t.Cleanup(func() { humanGateInteractive = original })

	repo := newSelfHostingRepo(t)
	w := selfHostingWorkflow(t, repo)
	e := newSelfHostingEngine(t, w, &selfHostingFakeProvider{commitLuna: true})
	e.In = strings.NewReader("y\ny\ny\ny\n")
	e.Out = io.Discard

	err := e.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "human gate self-host-review not confirmed") {
		t.Fatalf("human gate final acknowledgement error = %v", err)
	}
	if _, ok, resolveErr := e.Store.Resolve("human/self-host-review"); resolveErr != nil || ok {
		t.Fatalf("bad final acknowledgement wrote human evidence: ok=%v err=%v", ok, resolveErr)
	}
}
