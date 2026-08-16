package codex

import (
	"context"
	"reflect"
	"testing"

	"github.com/tdeshazo/agentflow-spec/provider"
)

func TestBuildArgs(t *testing.T) {
	got := buildArgs(provider.Request{
		Workspace: "/repo", Model: "gpt-test", Reasoning: "high",
		Sandbox: "workspace-write", Ephemeral: true, Color: "never",
	}, "/tmp/last")
	want := []string{
		"exec", "--cd", "/repo", "--sandbox", "workspace-write", "--ephemeral",
		"--color", "never", "--model", "gpt-test", "-c", `model_reasoning_effort="high"`,
		"--output-last-message", "/tmp/last", "-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRunRejectsUnsupportedApprovalPolicy(t *testing.T) {
	p := Provider{Binary: "does-not-matter"}
	_, err := p.Run(context.Background(), provider.Request{Workspace: t.TempDir(), Approval: "on-request"})
	if err == nil {
		t.Fatal("expected approval policy error")
	}
}
