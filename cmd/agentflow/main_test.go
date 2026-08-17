package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlanExpandedCLI(t *testing.T) {
	workflowFile := filepath.Join("..", "..", "internal", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = write
	runErr := runArgs([]string{"plan", "--expanded", "-f", workflowFile})
	_ = write.Close()
	os.Stdout = original
	output, _ := io.ReadAll(read)
	_ = read.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	for _, want := range []string{"resolvedLifecycle:", "safetyEnforcementPoints:", "recoveryBehavior:", "completionContract:"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("plan output missing %q:\n%s", want, output)
		}
	}
}

func TestStatusJSONCLI(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "AgentFlow Test"},
		{"config", "user.email", "agentflow@example.invalid"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	workflowFile := filepath.Join(filepath.Dir(source), "..", "..", "internal", "workflow", "testdata", "conformance", "valid", "minimal.yaml")
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = write
	runErr := runArgs([]string{"status", "--json", "-f", workflowFile, "-C", repo})
	_ = write.Close()
	os.Stdout = originalStdout
	output, readErr := io.ReadAll(read)
	_ = read.Close()
	if runErr != nil {
		t.Fatal(runErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}

	var status map[string]any
	if err := json.Unmarshal(output, &status); err != nil {
		t.Fatalf("CLI status JSON = %q: %v", output, err)
	}
	if status["state"] != "uninitialized" || status["initialized"] != false {
		t.Fatalf("CLI status JSON = %v", status)
	}
}
