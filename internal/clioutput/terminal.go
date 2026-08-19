package clioutput

import (
	"io"
	"os"
	"strconv"
	"strings"
)

// ColorLevel describes the conservative color policy available to a terminal.
// Unknown and none both disable styling; callers must not guess when a
// capability is not established.
type ColorLevel uint8

const (
	ColorUnknown ColorLevel = iota
	ColorNone
	ColorBasic
	Color256
	ColorTrueColor
)

// Enabled reports whether the level permits terminal styling.
func (l ColorLevel) Enabled() bool {
	return l >= ColorBasic
}

// TerminalProfile centralizes the terminal characteristics used by human
// presentation. Width zero means unknown. Detection is environment and file
// metadata only; it never probes the terminal or waits for a response.
type TerminalProfile struct {
	TTY         bool
	Interactive bool
	Color       ColorLevel
	Width       int
	Unicode     bool
	Hyperlinks  bool
}

// DetectTerminalProfile discovers terminal capabilities without terminal I/O.
// Passing a nil input treats a terminal output as presentation-interactive;
// callers with a real input stream should pass it to get the stricter check.
func DetectTerminalProfile(in io.Reader, out io.Writer) TerminalProfile {
	tty := IsTTY(out)
	profile := terminalProfileForTTY(tty)
	profile.Interactive = tty && (in == nil || IsTTYReader(in))
	return profile
}

// DetectTerminal is the output-only convenience form used by presentation
// callers. Unknown input capabilities do not block terminal output styling.
func DetectTerminal(out io.Writer) TerminalProfile {
	return DetectTerminalProfile(nil, out)
}

// terminalProfileForTTY builds the output-side capabilities used by all
// presenter constructors. It deliberately uses only bounded environment and
// descriptor hints; terminal queries are not part of the default path.
func terminalProfileForTTY(tty bool) TerminalProfile {
	if !tty {
		return TerminalProfile{Color: ColorNone}
	}
	return TerminalProfile{
		TTY:         true,
		Interactive: true,
		Color:       detectColorLevel(true),
		Width:       detectWidth(true),
		Unicode:     detectUnicode(true),
		Hyperlinks:  detectHyperlinks(true),
	}
}

func detectColorLevel(tty bool) ColorLevel {
	if !tty || !colorAllowed() {
		return ColorNone
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return ColorNone
	}
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		return ColorTrueColor
	}
	if strings.Contains(strings.ToLower(os.Getenv("TERM")), "256color") {
		return Color256
	}
	return ColorBasic
}

func detectWidth(tty bool) int {
	if !tty {
		return 0
	}
	// COLUMNS is a bounded, non-blocking hint. ioctl probing is deliberately
	// omitted because terminal discovery must remain safe in automation.
	width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS")))
	if err != nil || width < 20 || width > 1000 {
		return 0
	}
	return width
}

func detectUnicode(tty bool) bool {
	if !tty {
		return false
	}
	locale := os.Getenv("LC_ALL")
	if locale == "" {
		locale = os.Getenv("LC_CTYPE")
	}
	if locale == "" {
		locale = os.Getenv("LANG")
	}
	locale = strings.ToLower(locale)
	if locale == "c" || locale == "posix" {
		return false
	}
	return strings.Contains(locale, "utf-8") || strings.Contains(locale, "utf8")
}

func detectHyperlinks(tty bool) bool {
	if !tty || strings.EqualFold(strings.TrimSpace(os.Getenv("TERM")), "dumb") {
		return false
	}
	if value, ok := os.LookupEnv("AGENTFLOW_HYPERLINKS"); ok {
		return strings.EqualFold(strings.TrimSpace(value), "1") || strings.EqualFold(strings.TrimSpace(value), "true")
	}
	// These environments identify terminals with documented OSC-8 support.
	knownTerminal := os.Getenv("WT_SESSION") != "" || os.Getenv("KITTY_WINDOW_ID") != ""
	knownTerminal = knownTerminal || os.Getenv("VTE_VERSION") != ""
	knownTerminal = knownTerminal || os.Getenv("GHOSTTY_RESOURCES_DIR") != ""
	if knownTerminal {
		return true
	}
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "apple_terminal", "hyper", "iterm.app", "vscode", "wezterm":
		return true
	default:
		return false
	}
}
