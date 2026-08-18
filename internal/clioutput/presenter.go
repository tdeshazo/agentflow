package clioutput

import (
	"fmt"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// Role identifies a semantic presentation role for human-facing terminal text.
type Role int

const (
	RolePlain Role = iota
	RoleHeading
	RoleLabel
	RoleSuccess
	RoleWarning
	RoleError
	RoleAccent
	RoleMuted
)

const ansiReset = "\x1b[0m"

// Presenter applies terminal-only semantic styling without changing the
// underlying text contract for redirected or buffered output.
type Presenter struct {
	Out   io.Writer
	TTY   bool
	Color bool
}

// NewPresenter builds a presenter from the actual output destination. ANSI
// styling is disabled when NO_COLOR is present in the environment.
func NewPresenter(out io.Writer) Presenter {
	tty := IsTTY(out)
	return NewPresenterWithMode(out, tty, tty && colorAllowed())
}

func colorAllowed() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor
}

// NewPresenterWithMode is the deterministic presentation seam used by tests.
func NewPresenterWithMode(out io.Writer, tty, color bool) Presenter {
	if out == nil {
		out = io.Discard
	}
	return Presenter{Out: out, TTY: tty, Color: tty && color}
}

// ColorEnabled reports whether ANSI styling should be used for out.
func ColorEnabled(out io.Writer) bool {
	return NewPresenter(out).Color
}

// IsTTYReader reports whether r is a terminal-backed input file.
func IsTTYReader(r io.Reader) bool {
	file, ok := r.(*os.File)
	if !ok || file == nil {
		return false
	}
	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// IsInteractive reports whether a human can interact through terminal-backed
// input and output. Pipes, files, and test buffers remain non-interactive.
func IsInteractive(in io.Reader, out io.Writer) bool {
	return IsTTYReader(in) && IsTTY(out)
}

// Style returns text decorated for its semantic role when terminal color is
// enabled. The original text is returned byte-for-byte otherwise.
func (p Presenter) Style(role Role, text string) string {
	if !p.Color || text == "" || role == RolePlain {
		return text
	}
	code := ""
	switch role {
	case RoleHeading:
		code = "\x1b[1;36m"
	case RoleLabel:
		code = "\x1b[36m"
	case RoleSuccess:
		code = "\x1b[32m"
	case RoleWarning:
		code = "\x1b[33m"
	case RoleError:
		code = "\x1b[31m"
	case RoleAccent:
		code = "\x1b[1;34m"
	case RoleMuted:
		code = "\x1b[2m"
	}
	if code == "" {
		return text
	}
	return code + text + ansiReset
}

// Line writes one formatted line with semantic styling.
func (p Presenter) Line(role Role, format string, args ...any) {
	fmt.Fprintln(p.Out, p.Style(role, fmt.Sprintf(format, args...)))
}

// Print writes formatted text without adding a newline.
func (p Presenter) Print(role Role, format string, args ...any) {
	fmt.Fprint(p.Out, p.Style(role, fmt.Sprintf(format, args...)))
}

// StateRole maps durable status values to a semantic role for human display.
func StateRole(state string) Role {
	switch state {
	case "ready", "completed", "running":
		return RoleSuccess
	case "human-gated", "validation-failed/recoverable", "not_running":
		return RoleWarning
	case "safety-failed/terminal", "malformed":
		return RoleError
	default:
		return RoleAccent
	}
}
