package engine

import (
	"bytes"
	"strings"
	"testing"
)

func TestStatusToTTYStylesHumanOutputWithoutChangingPlainText(t *testing.T) {
	repo := newSelfHostingRepo(t)
	e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), &selfHostingFakeProvider{})

	var plain bytes.Buffer
	if err := e.StatusTo(&plain, false, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain status contains ANSI: %q", plain.String())
	}
	if !strings.Contains(plain.String(), "state: uninitialized") {
		t.Fatalf("plain status = %q", plain.String())
	}

	var styled bytes.Buffer
	if err := e.StatusTo(&styled, true, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled.String(), "\x1b[") || !strings.Contains(styled.String(), "uninitialized") {
		t.Fatalf("styled status = %q", styled.String())
	}

	var noColor bytes.Buffer
	if err := e.StatusTo(&noColor, true, false); err != nil {
		t.Fatal(err)
	}
	if got := noColor.String(); got != plain.String() {
		t.Fatalf("TTY no-color status changed text contract:\nplain=%q\nno-color=%q", plain.String(), got)
	}
}

func TestStatusSnapshotFailsClosedForStaleAndMalformedActiveState(t *testing.T) {
	t.Run("stale active state has no recovery advice", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), &selfHostingFakeProvider{})
		head, err := e.Repo.Head()
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Store.SetJSON(e.activeRecord(), ActivePhase{
			PhaseID:     "implement",
			StartCommit: head,
			FailureKind: PhaseFailureSafety,
		}); err != nil {
			t.Fatal(err)
		}

		snapshot, err := e.statusSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.State != "stale" || snapshot.Recovery != "" || snapshot.NextAction != "" {
			t.Fatalf("stale status fabricated recovery advice: %+v", snapshot)
		}
	})

	t.Run("missing active start commit is rejected", func(t *testing.T) {
		repo := newSelfHostingRepo(t)
		e := newSelfHostingEngine(t, selfHostingWorkflow(t, repo), &selfHostingFakeProvider{})
		if err := e.Store.SetJSON(e.activeRecord(), ActivePhase{PhaseID: "implement", FailureKind: PhaseFailureSafety}); err != nil {
			t.Fatal(err)
		}
		e.recoveryEligible = true

		if _, err := e.statusSnapshot(); err == nil || !strings.Contains(err.Error(), "has no start commit") {
			t.Fatalf("malformed active state error = %v", err)
		}
		if guidance := e.FailureRecoveryGuidance(); guidance != "" {
			t.Fatalf("malformed active state guidance = %q", guidance)
		}
	})
}
