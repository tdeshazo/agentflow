package clioutput

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDetectTerminalProfileUsesConservativeFallbacks(t *testing.T) {
	profile := DetectTerminalProfile(strings.NewReader("input\n"), &bytes.Buffer{})

	if profile.TTY || profile.Interactive || profile.Unicode || profile.Hyperlinks {
		t.Fatalf("buffer profile claimed terminal capabilities: %+v", profile)
	}
	if profile.Color != ColorNone || profile.Width != 0 {
		t.Fatalf("buffer profile = %+v, want plain unknown capabilities", profile)
	}
}

func TestTerminalProfileEnvironmentHintsAreBounded(t *testing.T) {
	oldNoColor, hadNoColor := os.LookupEnv("NO_COLOR")
	if err := os.Unsetenv("NO_COLOR"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if hadNoColor {
			_ = os.Setenv("NO_COLOR", oldNoColor)
			return
		}
		_ = os.Unsetenv("NO_COLOR")
	})
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("COLUMNS", "120")
	t.Setenv("LC_ALL", "C.UTF-8")
	t.Setenv("AGENTFLOW_HYPERLINKS", "true")

	if got := detectColorLevel(true); got != ColorTrueColor {
		t.Fatalf("color level = %v, want truecolor", got)
	}
	if got := detectWidth(true); got != 120 {
		t.Fatalf("width = %d, want 120", got)
	}
	unicode := detectUnicode(true)
	hyperlinks := detectHyperlinks(true)
	if !unicode || !hyperlinks {
		t.Fatalf("safe environment hints were not detected: unicode=%v hyperlinks=%v", unicode, hyperlinks)
	}

	t.Setenv("COLUMNS", "not-a-width")
	t.Setenv("LC_ALL", "C")
	t.Setenv("AGENTFLOW_HYPERLINKS", "false")
	width := detectWidth(true)
	unicode = detectUnicode(true)
	hyperlinks = detectHyperlinks(true)
	if width != 0 || unicode || hyperlinks {
		t.Fatalf(
			"invalid or conservative hints produced capabilities: width=%d unicode=%v hyperlinks=%v",
			width,
			unicode,
			hyperlinks,
		)
	}
}
