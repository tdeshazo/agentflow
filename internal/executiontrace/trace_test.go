package executiontrace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/tdeshazo/agentflow/internal/gitstate"
)

func TestStoreAppendsVersionedMonotonicEventsAcrossReopen(t *testing.T) {
	repo := gitstate.Repo{Root: t.TempDir()}
	initTraceRepo(t, repo.Root)
	store, err := Open(repo, "run_00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(Event{Event: "run_started"}); err != nil {
		t.Fatal(err)
	}
	path := store.Path
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(repo, "run_00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(Event{Event: "node_started", NodeID: "build", NodeExecutionID: "node_a", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var count uint64
	for wantSequence := uint64(1); scanner.Scan(); wantSequence++ {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event.SchemaVersion != SchemaVersion || event.Sequence != wantSequence || event.RunID != "run_00112233445566778899aabbccddeeff" {
			t.Fatalf("event = %+v", event)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event count = %d, want 2", count)
	}
}

func TestOpenRejectsIncompatibleExistingTrace(t *testing.T) {
	repo := gitstate.Repo{Root: t.TempDir()}
	initTraceRepo(t, repo.Root)
	runID := "run_ffeeddccbbaa99887766554433221100"
	path, err := Path(repo, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo.Root+"/.git/agentflow/traces", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"sequence":1,"time":"now","run_id":"`+runID+`","event":"run_started"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(repo, runID); err == nil {
		t.Fatal("Open accepted an incompatible trace schema")
	}
}

func TestOpenDiscardsTornFinalEvent(t *testing.T) {
	repo := gitstate.Repo{Root: t.TempDir()}
	initTraceRepo(t, repo.Root)
	runID := "run_ffeeddccbbaa99887766554433221100"
	path, err := Path(repo, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo.Root+"/.git/agentflow/traces", 0o700); err != nil {
		t.Fatal(err)
	}
	complete := `{"schema_version":1,"sequence":1,"time":"now","run_id":"` + runID + `","event":"run_started"}` + "\n"
	if err := os.WriteFile(path, []byte(complete+`{"schema_version":1,"sequence":2`), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(repo, runID)
	if err != nil {
		t.Fatalf("Open rejected a trace with a torn final event: %v", err)
	}
	if err := store.Append(Event{Event: "run_resumed"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(contents, []byte{'\n'}), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("event count = %d, want 2; trace = %q", len(lines), contents)
	}
	var resumed Event
	if err := json.Unmarshal(lines[1], &resumed); err != nil {
		t.Fatalf("decode resumed event: %v", err)
	}
	if resumed.Sequence != 2 || resumed.Event != "run_resumed" {
		t.Fatalf("resumed event = %+v", resumed)
	}
}

func TestOpenRejectsMalformedCompletedEvent(t *testing.T) {
	repo := gitstate.Repo{Root: t.TempDir()}
	initTraceRepo(t, repo.Root)
	runID := "run_ffeeddccbbaa99887766554433221100"
	path, err := Path(repo, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo.Root+"/.git/agentflow/traces", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"sequence":1`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(repo, runID); err == nil {
		t.Fatal("Open accepted a malformed completed event")
	}
}

func TestReadRecentReturnsBoundedChronologicalEvents(t *testing.T) {
	repo := gitstate.Repo{Root: t.TempDir()}
	initTraceRepo(t, repo.Root)
	runID := "run_0123456789abcdeffedcba9876543210"
	store, err := Open(repo, runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"one", "two", "three", "four", "five"} {
		if err := store.Append(Event{Event: kind}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	recent, err := ReadRecent(repo, runID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if recent.EventCount != 5 || len(recent.Events) != 3 {
		t.Fatalf("recent trace = %+v", recent)
	}
	for index, want := range []string{"three", "four", "five"} {
		if got := recent.Events[index]; got.Event != want || got.Sequence != uint64(index+3) {
			t.Fatalf("event %d = %+v, want %q at sequence %d", index, got, want, index+3)
		}
	}
}

func TestReadRecentIgnoresTornFinalEventWithoutMutation(t *testing.T) {
	repo := gitstate.Repo{Root: t.TempDir()}
	initTraceRepo(t, repo.Root)
	runID := "run_0123456789abcdeffedcba9876543210"
	path, err := Path(repo, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repo.Root+"/.git/agentflow/traces", 0o700); err != nil {
		t.Fatal(err)
	}
	complete := `{"schema_version":1,"sequence":1,"time":"now","run_id":"` + runID + `","event":"run_started"}` + "\n"
	contents := []byte(complete + `{"schema_version":1,"sequence":2`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	recent, err := ReadRecent(repo, runID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if recent.EventCount != 1 || len(recent.Events) != 1 || recent.Events[0].Event != "run_started" {
		t.Fatalf("recent trace = %+v", recent)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, contents) {
		t.Fatal("ReadRecent mutated a torn trace")
	}
}

func initTraceRepo(t *testing.T, root string) {
	t.Helper()
	if output, err := exec.Command("git", "-C", root, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}
