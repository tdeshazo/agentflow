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

func TestPresenterUsesUnicodeAndASCIIStatusGlyphs(t *testing.T) {
	tests := []struct {
		name      string
		unicode   bool
		wantGlyph string
	}{
		{name: "unicode terminal", unicode: true, wantGlyph: "▸ ==>"},
		{name: "ascii terminal", unicode: false, wantGlyph: "> ==>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			p := NewPresenterWithProfile(&output, PresentationRich, TerminalProfile{
				TTY:     true,
				Color:   ColorBasic,
				Unicode: test.unicode,
			})
			p.PhaseStart("build", "Build")
			if !strings.Contains(output.String(), test.wantGlyph) {
				t.Fatalf("glyph output = %q, want %q", output.String(), test.wantGlyph)
			}
		})
	}
}

func TestPresenterHyperlinksRequireAnExplicitSafeProfile(t *testing.T) {
	tests := []struct {
		name     string
		safe     bool
		mode     PresentationMode
		wantOSC8 bool
	}{
		{name: "safe rich terminal", safe: true, mode: PresentationRich, wantOSC8: true},
		{name: "unknown terminal", safe: false, mode: PresentationRich, wantOSC8: false},
		{name: "raw boundary", safe: true, mode: PresentationRaw, wantOSC8: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := NewPresenterWithProfile(&bytes.Buffer{}, test.mode, TerminalProfile{
				TTY:        true,
				Color:      ColorBasic,
				Hyperlinks: test.safe,
			})
			got := p.Hyperlink("repo", "file:///tmp/repo")
			if strings.Contains(got, "repo") != true || strings.Contains(got, "\x1b]8;;") != test.wantOSC8 {
				t.Fatalf("hyperlink = %q, wantOSC8=%v", got, test.wantOSC8)
			}
		})
	}

	p := NewPresenterWithProfile(&bytes.Buffer{}, PresentationRich, TerminalProfile{TTY: true, Hyperlinks: true})
	if got := p.Hyperlink("repo", "file:///tmp/repo\nunsafe"); got != "repo" {
		t.Fatalf("unsafe hyperlink target was emitted: %q", got)
	}
}

func TestPresenterGitSummaryIsCompactAndPortable(t *testing.T) {
	files := []string{"README.md", "internal/clioutput/presenter.go"}

	var rich bytes.Buffer
	p := NewPresenterWithProfile(&rich, PresentationRich, TerminalProfile{
		TTY:     true,
		Color:   ColorBasic,
		Unicode: true,
	})
	p.GitSummary("since base", files)
	if got := rich.String(); !strings.Contains(got, "Git since base: 2 files changed") ||
		strings.Contains(got, "README.md") {
		t.Fatalf("rich Git summary = %q", got)
	}

	var plain bytes.Buffer
	NewPresenterWithProfile(&plain, PresentationPlain, TerminalProfile{}).GitSummary("since base", files)
	if got := plain.String(); got != "==> Git since base: 2 files changed\n" {
		t.Fatalf("plain Git summary = %q", got)
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
