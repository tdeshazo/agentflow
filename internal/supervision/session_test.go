package supervision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

func TestSessionAttachIsExclusiveAndForwardsApprovedInputAndSignals(t *testing.T) {
	repo := newSessionRepo(t)
	workflow := "supervised"
	runID := "run_0123456789abcdef0123456789abcdef"
	process := gitstate.CurrentProcessMetadata()
	if process == nil {
		t.Skip("stable process metadata is unavailable")
	}
	store := gitstate.NewStore(repo, workflow)
	if err := store.SetJSON("run-identity", map[string]any{"version": 2, "run_id": runID}); err != nil {
		t.Fatal(err)
	}
	descriptor := gitstate.NewDescriptor(workflow, "", gitstate.RecordNames{})
	descriptor.Process = process
	if err := store.SetJSON(gitstate.DescriptorRecord, descriptor); err != nil {
		t.Fatal(err)
	}
	cancelled := make(chan struct{}, 1)
	server, err := Start(repo, workflow, runID, func() { cancelled <- struct{}{} })
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("Unix-domain session endpoints are unavailable in this sandbox: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })

	client, err := Attach(context.Background(), repo, workflow)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := Attach(context.Background(), repo, workflow); err == nil || !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("concurrent attach error = %v", err)
	}
	if err := client.SendInput("yes\n"); err != nil {
		t.Fatal(err)
	}
	server.EnableInput(true)
	// The first input was sent before a human gate was enabled and must not be
	// buffered for a later gate.
	if err := client.SendInput("yes\n"); err != nil {
		t.Fatal(err)
	}
	input := make(chan string, 1)
	go func() {
		b := make([]byte, 4)
		n, readErr := server.Input().Read(b)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			input <- "error: " + readErr.Error()
			return
		}
		input <- string(b[:n])
	}()
	select {
	case got := <-input:
		if got != "yes\n" {
			t.Fatalf("forwarded input = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("approved attached input was not forwarded")
	}
	if err := client.SendSignal("interrupt"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("attached interrupt did not cancel run")
	}
}

func TestAttachRejectsStaleOrMismatchedDurableIdentity(t *testing.T) {
	repo := newSessionRepo(t)
	workflow := "stale"
	runID := "run_0123456789abcdef0123456789abcdef"
	metadataPath, socketPath, err := paths(repo, workflow)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepareDirectory(filepath.Dir(metadataPath)); err != nil {
		t.Fatal(err)
	}
	metadata := Metadata{Version: metadataVersion, Workflow: workflow, RunID: runID, Process: &gitstate.ProcessMetadata{PID: 99999999, Start: "1"}, Socket: socketPath}
	if err := os.WriteFile(metadataPath, mustJSON(t, metadata), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Attach(context.Background(), repo, workflow); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("stale attach error = %v", err)
	}
}

func TestLongRepositoryAndWorkflowUseShortAttachableEndpoint(t *testing.T) {
	if _, err := sessionEndpointPath(strings.Repeat("a", 32)); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("session IPC unavailable: %v", err)
		}
		t.Fatal(err)
	}
	root := t.TempDir()
	for i := 0; i < 5; i++ {
		root = filepath.Join(root, "representative-long-repository-directory-name")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	repo := gitstate.Repo{Root: root}
	workflow := "representative-long-workflow-name-" + strings.Repeat("w", 48)
	runID := "run_9123456789abcdef0123456789abcdef"
	server := installLiveSession(t, repo, workflow, runID)
	defer server.Close()
	if len([]byte(server.metadata.Socket)) > unixSocketPathMax {
		t.Fatalf("socket path is %d bytes: %q", len([]byte(server.metadata.Socket)), server.metadata.Socket)
	}
	if strings.Contains(server.metadata.Socket, workflow) || strings.Contains(server.metadata.Socket, filepath.Base(root)) {
		t.Fatalf("socket path embeds repository/workflow names: %q", server.metadata.Socket)
	}
	client, err := Attach(context.Background(), repo, workflow, 0)
	if err != nil {
		t.Fatalf("attach over short endpoint: %v", err)
	}
	defer client.Close()
	if client.RunID() != runID {
		t.Fatalf("attached run = %q, want %q", client.RunID(), runID)
	}
}

func TestCloseUnblocksIncompleteHandshake(t *testing.T) {
	repo, server := newLiveSession(t, "handshake", "run_1123456789abcdef0123456789abcdef")
	conn, err := net.Dial("unix", server.metadata.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() { done <- server.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server shutdown blocked on an unauthenticated connection")
	}
	_ = repo
}

func TestReplayIsPerRunAndLiveDeliverySurvivesReplayCapacity(t *testing.T) {
	repo, first := newLiveSession(t, "replay", "run_2123456789abcdef0123456789abcdef")
	first.Publish("stdout", []byte("old-run\n"))
	client, err := Attach(context.Background(), repo, "replay", 10)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := client.Receive()
	if err != nil || string(frame.Output) != "old-run\n" {
		t.Fatalf("first replay = %+v, err=%v", frame, err)
	}
	_ = client.Detach()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := installLiveSession(t, repo, "replay", "run_3123456789abcdef0123456789abcdef")
	defer second.Close()
	second.Publish("stderr", make([]byte, maxReplayBytes+1))
	client, err = Attach(context.Background(), repo, "replay", 10)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	second.Publish("stdout", []byte("new-live\n"))
	frame, err = client.Receive()
	if err != nil || string(frame.Output) != "new-live\n" {
		t.Fatalf("new run live output = %+v, err=%v", frame, err)
	}
}

func TestCompletionFrameFollowsFinalOutputCursor(t *testing.T) {
	repo, server := newLiveSession(t, "complete", "run_4123456789abcdef0123456789abcdef")
	defer server.Close()
	client, err := Attach(context.Background(), repo, "complete", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server.Publish("stdout", []byte("final\n"))
	server.Complete("success")
	output, err := client.Receive()
	if err != nil || string(output.Output) != "final\n" {
		t.Fatalf("final output = %+v, err=%v", output, err)
	}
	completion, err := client.Receive()
	if err != nil || !completion.Completed || completion.Result != "success" || completion.Cursor != output.Cursor {
		t.Fatalf("completion = %+v, err=%v", completion, err)
	}
}

func TestExplicitDetachReleasesTerminalWithoutReplacingRun(t *testing.T) {
	repo, server := newLiveSession(t, "handoff", "run_7123456789abcdef0123456789abcdef")
	defer server.Close()
	foreground, err := Attach(context.Background(), repo, "handoff", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := foreground.Detach(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		detachedAgain, attachErr := Attach(context.Background(), repo, "handoff", 0)
		if attachErr == nil {
			defer detachedAgain.Close()
			if detachedAgain.RunID() != foreground.RunID() {
				t.Fatalf("run changed across handoff: %q != %q", detachedAgain.RunID(), foreground.RunID())
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("reattach after explicit detach: %v", attachErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestInputGenerationDoesNotBleedAcrossGates(t *testing.T) {
	_, server := newLiveSession(t, "gates", "run_5123456789abcdef0123456789abcdef")
	defer server.Close()
	server.EnableInput(true)
	server.deliverInput([]byte("old\n"))
	server.EnableInput(false)
	server.EnableInput(true)
	server.deliverInput([]byte("new\n"))
	b := make([]byte, 16)
	n, err := server.Input().Read(b)
	if err != nil || string(b[:n]) != "new\n" {
		t.Fatalf("next gate input = %q, err=%v", b[:n], err)
	}
}

func TestSlowAttachmentNeverBlocksPublishAndSaturationStopsWriter(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer clientSide.Close()
	peer := newConnection(serverSide)
	server := &Server{active: peer, attached: true, connections: map[*connection]struct{}{peer: {}}, replay: make([]replayChunk, 0)}
	started := time.Now()
	for i := 0; i < maxOutgoingFrames+10; i++ {
		server.Publish("stdout", []byte("x"))
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("publish blocked behind slow client for %s", elapsed)
	}
	server.mu.Lock()
	active := server.active
	server.mu.Unlock()
	if active != nil {
		t.Fatal("saturated attachment remained active")
	}
	peer.abort()
}

func TestConcurrentOutputUsesStrictCursorOrder(t *testing.T) {
	repo, server := newLiveSession(t, "ordered", "run_8123456789abcdef0123456789abcdef")
	defer server.Close()
	client, err := Attach(context.Background(), repo, "ordered", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	const count = 100
	var publishers sync.WaitGroup
	publishers.Add(2)
	for _, stream := range []string{"stdout", "stderr"} {
		stream := stream
		go func() {
			defer publishers.Done()
			for i := 0; i < count/2; i++ {
				server.Publish(stream, []byte("x"))
			}
		}()
	}
	publishers.Wait()
	var previous uint64
	for i := 0; i < count; i++ {
		frame, err := client.Receive()
		if err != nil {
			t.Fatal(err)
		}
		if frame.Cursor != previous+1 {
			t.Fatalf("cursor[%d] = %d, want %d", i, frame.Cursor, previous+1)
		}
		previous = frame.Cursor
	}
}

func newLiveSession(t *testing.T, workflow, runID string) (gitstate.Repo, *Server) {
	t.Helper()
	repo := newSessionRepo(t)
	return repo, installLiveSession(t, repo, workflow, runID)
}

func installLiveSession(t *testing.T, repo gitstate.Repo, workflow, runID string) *Server {
	t.Helper()
	process := gitstate.CurrentProcessMetadata()
	if process == nil {
		t.Skip("stable process metadata is unavailable")
	}
	store := gitstate.NewStore(repo, workflow)
	if err := store.SetJSON("run-identity", map[string]any{"version": 2, "run_id": runID}); err != nil {
		t.Fatal(err)
	}
	descriptor := gitstate.NewDescriptor(workflow, "", gitstate.RecordNames{})
	descriptor.Process = process
	if err := store.SetJSON(gitstate.DescriptorRecord, descriptor); err != nil {
		t.Fatal(err)
	}
	server, err := Start(repo, workflow, runID, func() {})
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("session IPC unavailable: %v", err)
		}
		t.Fatal(err)
	}
	return server
}

func newSessionRepo(t *testing.T) gitstate.Repo {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	return gitstate.Repo{Root: dir}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
