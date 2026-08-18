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

func TestNoColorEnvironmentDisablesColorPolicy(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if colorAllowed() {
		t.Fatal("NO_COLOR presence did not disable ANSI color policy")
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

func TestStateRole(t *testing.T) {
	for state, want := range map[string]Role{
		"ready":                         RoleSuccess,
		"completed":                     RoleSuccess,
		"human-gated":                   RoleWarning,
		"validation-failed/recoverable": RoleWarning,
		"safety-failed/terminal":        RoleError,
		"malformed":                     RoleError,
		"active":                        RoleAccent,
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
