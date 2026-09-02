package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestStatusDetailReturnsBoundedTraceForHumansAndAutomation(t *testing.T) {
	repo := newDurableRepo(t)
	e := newDurableEngine(t, durableWorkflow(repo, "status-detail"), &durableProvider{})
	if err := e.initializeOrResumeState(); err != nil {
		t.Fatal(err)
	}
	if err := e.startExecutionTrace(); err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= statusDetailEventLimit+5; index++ {
		e.traceEvent("node_state_transition", map[string]string{
			"phase": "change", "transition": fmt.Sprintf("step-%d", index),
		})
	}

	var jsonOutput bytes.Buffer
	if err := e.StatusJSONWithDetailTo(&jsonOutput, false); err != nil {
		t.Fatal(err)
	}
	var report StatusReport
	if err := json.Unmarshal(jsonOutput.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Detail == nil || report.Detail.TraceState != "available" {
		t.Fatalf("status detail = %+v", report.Detail)
	}
	if report.Detail.EventCount != uint64(statusDetailEventLimit+5) || report.Detail.EventLimit != statusDetailEventLimit || !report.Detail.EventsTruncated {
		t.Fatalf("status detail counts = %+v", report.Detail)
	}
	if len(report.Detail.RecentEvents) != statusDetailEventLimit {
		t.Fatalf("recent event count = %d, want %d", len(report.Detail.RecentEvents), statusDetailEventLimit)
	}
	if first := report.Detail.RecentEvents[0]; first.Sequence != 6 || first.Fields["transition"] != "step-6" {
		t.Fatalf("first recent event = %+v", first)
	}
	if last := report.Detail.RecentEvents[len(report.Detail.RecentEvents)-1]; last.Sequence != uint64(statusDetailEventLimit+5) || last.Fields["transition"] != "step-25" {
		t.Fatalf("last recent event = %+v", last)
	}

	var textOutput bytes.Buffer
	if err := e.StatusWithDetailTo(&textOutput, false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"detail:\n",
		"  trace_state: available\n",
		"  event_count: 25\n",
		"  events_truncated: true\n",
		`sequence=6`,
		`event="node_state_transition"`,
		`field.transition="step-25"`,
	} {
		if !strings.Contains(textOutput.String(), want) {
			t.Fatalf("detailed text status missing %q:\n%s", want, textOutput.String())
		}
	}
}

func TestStatusDetailReportsTraceAvailabilityWithoutFailingSummary(t *testing.T) {
	tests := []struct {
		name       string
		initialize bool
		corrupt    bool
		wantState  string
	}{
		{name: "uninitialized", wantState: "not_initialized"},
		{name: "missing", initialize: true, wantState: "missing"},
		{name: "invalid", initialize: true, corrupt: true, wantState: "error"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newDurableRepo(t)
			e := newDurableEngine(t, durableWorkflow(repo, "status-detail-"+test.name), &durableProvider{})
			if test.initialize {
				if err := e.initializeOrResumeState(); err != nil {
					t.Fatal(err)
				}
			}
			if test.corrupt {
				if err := e.startExecutionTrace(); err != nil {
					t.Fatal(err)
				}
				path := e.traceStore.Path
				if err := e.traceStore.Close(); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not-json\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			var output bytes.Buffer
			if err := e.StatusJSONWithDetailTo(&output, false); err != nil {
				t.Fatal(err)
			}
			var report StatusReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Detail == nil || report.Detail.TraceState != test.wantState || report.Detail.RecentEvents == nil {
				t.Fatalf("status detail = %+v", report.Detail)
			}
			if test.corrupt && report.Detail.TraceError == "" {
				t.Fatal("invalid trace detail omitted its diagnostic")
			}
		})
	}
}
