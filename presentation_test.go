package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

func TestValidationPresentationUsesSemanticOutcomeRoles(t *testing.T) {
	tests := []struct {
		name   string
		status workflow.Status
		want   string
		err    bool
	}{
		{name: "executable", status: workflow.Executable, want: "valid and executable", err: false},
		{name: "unsupported", status: workflow.Unsupported, want: "valid but unsupported by this runtime", err: false},
		{name: "invalid", status: workflow.Invalid, want: "invalid", err: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			result := workflow.Result{
				Status: test.status,
				Diagnostics: []workflow.Diagnostic{{
					Status:  workflow.Invalid,
					Path:    "spec",
					Message: "example",
				}},
			}
			err := writeValidationResult(clioutput.NewPresenterWithMode(&output, true, true), result)
			if (err != nil) != test.err {
				t.Fatalf("error = %v, want error=%v", err, test.err)
			}
			if !strings.Contains(output.String(), test.want) || !strings.Contains(output.String(), "\x1b[") {
				t.Fatalf("styled validation output = %q", output.String())
			}
		})
	}
}

func TestValidationPresentationPreservesNonTTYText(t *testing.T) {
	result := workflow.Result{
		Status: workflow.Invalid,
		Diagnostics: []workflow.Diagnostic{{
			Status:  workflow.Invalid,
			Path:    "spec",
			Message: "example",
		}},
	}

	var output bytes.Buffer
	err := writeValidationResult(clioutput.NewPresenterWithMode(&output, false, true), result)
	if err == nil || output.String() != "spec: invalid: example\ninvalid\n" {
		t.Fatalf("non-TTY validation output = %q, error = %v", output.String(), err)
	}
}

func TestUsageAndTopLevelErrorPresentation(t *testing.T) {
	var usage bytes.Buffer
	writeUsage(&usage, clioutput.NewPresenterWithMode(&usage, true, true))
	if !strings.Contains(usage.String(), "\x1b[") || !strings.Contains(usage.String(), "usage:") {
		t.Fatalf("styled usage = %q", usage.String())
	}

	var plain bytes.Buffer
	presenter := clioutput.NewPresenterWithMode(&plain, false, true)
	writeTopLevelError(&plain, presenter, errors.New("bad input"))
	if got := plain.String(); got != "agentflow: bad input\n" {
		t.Fatalf("plain top-level error = %q", got)
	}
}

func TestStatusAllTextTTYStylingPreservesNoColorText(t *testing.T) {
	repo := newCLIStatusRepo(t)

	var plain bytes.Buffer
	if err := runAllStatusTo(repo.Root, false, &plain, false, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Fatalf("plain status contains ANSI: %q", plain.String())
	}

	var styled bytes.Buffer
	if err := runAllStatusTo(repo.Root, false, &styled, true, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(styled.String(), "\x1b[") || !strings.Contains(styled.String(), "repository:") {
		t.Fatalf("styled status = %q", styled.String())
	}

	var noColor bytes.Buffer
	if err := runAllStatusTo(repo.Root, false, &noColor, true, false); err != nil {
		t.Fatal(err)
	}
	if got := noColor.String(); got != plain.String() {
		t.Fatalf("TTY no-color status changed text contract:\nplain=%q\nno-color=%q", plain.String(), got)
	}
}

func TestStatusAllJSONNeverContainsANSI(t *testing.T) {
	repo := newCLIStatusRepo(t)
	var out bytes.Buffer
	if err := runAllStatusTo(repo.Root, true, &out, true, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("JSON output contains ANSI: %q", out.String())
	}
	if !strings.Contains(out.String(), "\n  \"") {
		t.Fatalf("TTY JSON was not indented: %q", out.String())
	}
}
