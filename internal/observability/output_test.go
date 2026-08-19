package observability

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestOutputBridgeWritesDetachedOutputToNormalLogStore(t *testing.T) {
	repo := newLogRepo(t)
	store, err := Open(repo, "detached-output")
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := NewOutputBridge(store)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = bridge.Stdout(), bridge.Stderr()
	fmt.Fprintln(os.Stdout, "detached diagnostic")
	fmt.Fprintln(os.Stderr, "detached failure detail")
	os.Stdout, os.Stderr = originalStdout, originalStderr
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, _, err := Read(repo, "detached-output")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{"process_output", "detached diagnostic", "detached failure detail"} {
		if !strings.Contains(text, want) {
			t.Fatalf("durable detached output missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("durable detached output contains terminal presentation escapes: %s", text)
	}
}
