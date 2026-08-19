package clioutput

import (
	"bytes"
	"strings"
	"testing"
)

func TestPresenterStylesOnlyTTYColorMode(t *testing.T) {
	var styled bytes.Buffer
	p := NewPresenterWithMode(&styled, true, true)
	p.Line(RoleSuccess, "complete")
	if got := styled.String(); !strings.Contains(got, "\x1b[") || !strings.Contains(got, "complete") {
		t.Fatalf("styled output = %q", got)
	}

	var noColor bytes.Buffer
	p = NewPresenterWithMode(&noColor, true, false)
	p.Line(RoleSuccess, "complete")
	if got := noColor.String(); got != "complete\n" {
		t.Fatalf("TTY no-color output = %q", got)
	}

	var redirected bytes.Buffer
	p = NewPresenterWithMode(&redirected, false, true)
	p.Line(RoleSuccess, "complete")
	if got := redirected.String(); got != "complete\n" {
		t.Fatalf("non-TTY output = %q", got)
	}
}

func TestPresenterPresentationModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     PresentationMode
		tty      bool
		color    bool
		wantANSI bool
		wantText string
		wantRaw  bool
	}{
		{
			name:     "rich terminal",
			mode:     PresentationRich,
			tty:      true,
			color:    true,
			wantANSI: true,
			wantText: "==> Phase build: Build",
		},
		{
			name:     "plain buffer",
			mode:     PresentationPlain,
			wantText: "==> Phase build: Build",
		},
		{
			name:     "raw boundary",
			mode:     PresentationRaw,
			wantRaw:  true,
			wantText: "provider output\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			p := NewPresenterWithCapabilities(&output, test.mode, test.tty, test.color)
			p.PhaseStart("build", "Build")
			p.Rule("Lifecycle")
			p.KeyValue("state", "active")
			if test.wantRaw {
				p.Raw(test.wantText)
			}

			got := output.String()
			if test.wantRaw {
				if got != test.wantText {
					t.Fatalf("raw output = %q, want %q", got, test.wantText)
				}
				if strings.Contains(got, "==>") || strings.Contains(got, "===") || strings.Contains(got, "\x1b[") {
					t.Fatalf("raw boundary contains AgentFlow framing: %q", got)
				}
				return
			}
			if !strings.Contains(got, test.wantText) {
				t.Fatalf("output = %q, missing %q", got, test.wantText)
			}
			if strings.Contains(got, "provider output") {
				t.Fatalf("non-raw semantic output unexpectedly contained raw payload: %q", got)
			}
			if strings.Contains(got, "\x1b[") != test.wantANSI {
				t.Fatalf("ANSI=%v output = %q", test.wantANSI, got)
			}
		})
	}
}

func TestPresenterRichModeKeepsInteractivityWhenNoColorIsSet(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	var output bytes.Buffer
	p := NewPresenterWithTTY(&output, true)
	p.Line(RoleSuccess, "complete")

	if !p.TTY {
		t.Fatal("NO_COLOR disabled terminal capability")
	}
	if p.Color {
		t.Fatal("NO_COLOR did not disable Rich ANSI styling")
	}
	if got := output.String(); got != "complete\n" {
		t.Fatalf("NO_COLOR output = %q", got)
	}
}

func TestNoColorEnvironmentDisablesColorPolicy(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if colorAllowed() {
		t.Fatal("NO_COLOR presence did not disable ANSI color policy")
	}

	var output bytes.Buffer
	newPresenterWithPolicy(&output, true, true, true).Line(RoleSuccess, "complete")
	if got := output.String(); got != "complete\n" {
		t.Fatalf("NO_COLOR presentation = %q", got)
	}
}

func TestPresenterSemanticRoles(t *testing.T) {
	p := NewPresenterWithMode(&bytes.Buffer{}, true, true)
	for _, role := range []Role{RoleHeading, RoleLabel, RoleSuccess, RoleWarning, RoleError, RoleAccent, RoleMuted} {
		got := p.Style(role, "text")
		if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, ansiReset) {
			t.Fatalf("role %v did not produce ANSI styling: %q", role, got)
		}
	}
}

func TestPresenterSemanticFieldHelpers(t *testing.T) {
	var output bytes.Buffer
	p := NewPresenterWithMode(&output, true, true)

	if got := p.Label("state"); !strings.Contains(got, "state:") || !strings.Contains(got, "\x1b[") {
		t.Fatalf("styled label = %q", got)
	}
	if got := p.State("running"); !strings.Contains(got, "running") || !strings.Contains(got, "\x1b[") {
		t.Fatalf("styled state = %q", got)
	}

	plain := NewPresenterWithMode(&bytes.Buffer{}, false, true)
	if got := plain.Label("state"); got != "state:" {
		t.Fatalf("plain label = %q", got)
	}
	if got := plain.State("running"); got != "running" {
		t.Fatalf("plain state = %q", got)
	}
}

func TestStateRole(t *testing.T) {
	for state, want := range map[string]Role{
		"ready":                         RoleSuccess,
		"completed":                     RoleSuccess,
		"human-gated":                   RoleWarning,
		"validation-failed/recoverable": RoleWarning,
		"safety-failed/terminal":        RoleError,
		"malformed":                     RoleError,
		"active":                        RoleAccent,
		"uninitialized":                 RoleAccent,
	} {
		if got := StateRole(state); got != want {
			t.Fatalf("StateRole(%q) = %v, want %v", state, got, want)
		}
	}
}

func TestIsInteractiveRejectsBuffers(t *testing.T) {
	if IsInteractive(strings.NewReader("yes\n"), &bytes.Buffer{}) {
		t.Fatal("buffered input/output was treated as an interactive terminal")
	}
}
