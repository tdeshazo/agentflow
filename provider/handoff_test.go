package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

func completeHandoff(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(Handoff{Version: HandoffVersionV1, Status: "complete", Summary: "implemented", Changes: []HandoffChange{}, Findings: []HandoffFinding{}, Checks: []string{}, Risks: []string{}, Blockers: []string{}, NextActions: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseHandoffRejectsUnsafePayloads(t *testing.T) {
	for name, raw := range map[string][]byte{
		"unknown":   []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"x","changes":[],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[],"extra":true}`),
		"traversal": []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"x","changes":[{"path":"../secret","summary":"x"}],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[]}`),
		"secret":    []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"token=not-safe","changes":[],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseHandoff(raw); err == nil {
				t.Fatal("ParseHandoff succeeded")
			}
		})
	}
}

func TestParseHandoffBoundedAndCanonical(t *testing.T) {
	raw := completeHandoff(t)
	handoff, err := ParseHandoff(raw)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Status != "complete" {
		t.Fatalf("status = %q", handoff.Status)
	}
	tooLarge := append([]byte(strings.Repeat(" ", MaxHandoffBytes)), raw...)
	if _, err := ParseHandoff(tooLarge); err == nil {
		t.Fatal("oversized handoff succeeded")
	}
}

func TestParseHandoffRejectsCredentialSignaturesAndDecodedCredentials(t *testing.T) {
	for _, summary := range []string{
		"Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature",
		"-----BEGIN PRIVATE KEY-----",
		"ghp_abcdefghijklmnopqrstuvwxyz012345",
		"github_pat_abcdefghijklmnopqrstuvwxyz",
		"xoxb-1234567890-secret",
		"sk-proj-abcdefghijklmnopqrstuvwxyz",
		"AKIAABCDEFGHIJKLMNOP",
	} {
		handoff := Handoff{Version: HandoffVersionV1, Status: "complete", Summary: summary, Changes: []HandoffChange{}, Findings: []HandoffFinding{}, Checks: []string{}, Risks: []string{}, Blockers: []string{}, NextActions: []string{}}
		raw, err := json.Marshal(handoff)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseHandoff(raw); err == nil {
			t.Fatalf("ParseHandoff accepted credential signature %q", summary)
		}
	}

	raw := []byte(`{"version":"agentflow.dev/handoff/v1","status":"complete","summary":"credential-\u0073ecret-cobalt","changes":[],"findings":[],"checks":[],"risks":[],"blockers":[],"nextActions":[]}`)
	if _, err := ParseHandoffWithCredentials(raw, []string{"credential-secret-cobalt"}); err == nil {
		t.Fatal("ParseHandoffWithCredentials accepted JSON-escaped authorized credential")
	}
}

func TestParseHandoffRejectsNonLocalChangePaths(t *testing.T) {
	for _, changePath := range []string{
		"/etc/passwd",
		`C:\outside`,
		"C:/outside",
		"C:relative",
		`\\server\share\file`,
		"../outside",
		"src/../outside",
		`src\..\outside`,
	} {
		handoff := Handoff{Version: HandoffVersionV1, Status: "complete", Summary: "complete", Changes: []HandoffChange{{Path: changePath, Summary: "changed"}}, Findings: []HandoffFinding{}, Checks: []string{}, Risks: []string{}, Blockers: []string{}, NextActions: []string{}}
		raw, err := json.Marshal(handoff)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseHandoff(raw); err == nil {
			t.Fatalf("ParseHandoff accepted unsafe path %q", changePath)
		}
	}
}
