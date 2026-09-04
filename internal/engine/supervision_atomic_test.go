package engine

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/supervision"
	"github.com/tdeshazo/agentflow/provider"
)

func TestImmediateForegroundRunWaitsForInitialAttachment(t *testing.T) {
	repo := newDurableRepo(t)
	w := durableWorkflow(repo, "foreground-atomic")
	w.Spec.Flow = nil
	setup := newDurableEngine(t, w, &durableProvider{})
	if err := setup.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	head, err := setup.Repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := setup.Store.SetCommit(setup.workflowCompleteMarker(), head); err != nil {
		t.Fatal(err)
	}
	ready := make(chan SessionStatus, 1)
	attached := make(chan struct{})
	e, err := New(w, map[string]provider.Provider{"test": &durableProvider{}}, Options{Detached: true, SessionReady: func(status SessionStatus) error {
		ready <- status
		<-attached
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	e.In = strings.NewReader("")
	e.Out = io.Discard
	runDone := make(chan error, 1)
	go func() { runDone <- e.Run(context.Background()) }()
	status := <-ready
	if !status.Attachable {
		t.Skip("private supervised IPC is unavailable")
	}
	select {
	case err := <-runDone:
		t.Fatalf("run completed before foreground attachment: %v", err)
	default:
	}
	client, err := supervision.Attach(context.Background(), e.Repo, w.Metadata.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	close(attached)
	completion := make(chan supervision.Frame, 1)
	frameErr := make(chan error, 1)
	go func() {
		frame, err := client.Receive()
		if err != nil {
			frameErr <- err
			return
		}
		completion <- frame
	}()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("immediate run did not finish after attachment acknowledgement")
	}
	select {
	case frame := <-completion:
		if !frame.Completed || frame.Result != "success" {
			t.Fatalf("completion frame = %+v", frame)
		}
	case err := <-frameErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("foreground attachment missed immediate completion")
	}
}
