package clioutput

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

// PresentationMode controls whether AgentFlow owns the presentation of an
// output stream. Rich is reserved for an interactive terminal, Plain keeps
// stable human-readable bytes without styling, and Raw is a write-through
// boundary for provider, log, and machine-readable streams.
type PresentationMode uint8

const (
	PresentationPlain PresentationMode = iota
	PresentationRich
	PresentationRaw
)

// Short mode names are convenient at call sites that are already in the
// clioutput package's presentation vocabulary.
const (
	Plain = PresentationPlain
	Rich  = PresentationRich
	Raw   = PresentationRaw
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

// RoleProgress is an alias for the accent role used for active work and other
// in-progress values.
const RoleProgress = RoleAccent

const ansiReset = "\x1b[0m"

// Presenter applies terminal-only semantic styling without changing the
// underlying text contract for redirected or buffered output.
type Presenter struct {
	Out     io.Writer
	TTY     bool
	Color   bool
	Mode    PresentationMode
	Profile TerminalProfile
}

// NewPresenter builds a presenter from the actual output destination. ANSI
// styling is disabled when NO_COLOR is present in the environment.
func NewPresenter(out io.Writer) Presenter {
	profile := DetectTerminal(out)
	return NewPresenterWithProfile(out, presentationMode(profile.TTY), profile)
}

// NewPresenterWithTTY applies the real NO_COLOR policy with an explicit
// terminal mode. It is useful when the terminal state is supplied by a caller
// or test rather than inferred from the writer itself.
func NewPresenterWithTTY(out io.Writer, tty bool) Presenter {
	profile := DetectTerminal(out)
	profile.TTY = tty
	profile.Interactive = tty
	if !tty || !colorAllowed() {
		profile.Color = ColorNone
	}
	return NewPresenterWithProfile(out, presentationMode(tty), profile)
}

func colorAllowed() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return !noColor
}

// NewPresenterWithMode is the deterministic presentation seam used by tests.
func NewPresenterWithMode(out io.Writer, tty, color bool) Presenter {
	level := ColorNone
	if tty && color {
		level = ColorBasic
	}
	return NewPresenterWithProfile(out, presentationMode(tty), TerminalProfile{
		TTY:         tty,
		Interactive: tty,
		Color:       level,
		Unicode:     tty && color && detectUnicode(tty),
	})
}

// NewPresenterWithPresentation constructs a presenter for an explicit
// semantic mode. Rich implies an interactive terminal capability; use
// NewPresenterWithCapabilities when those capabilities need to be injected.
func NewPresenterWithPresentation(out io.Writer, mode PresentationMode) Presenter {
	profile := DetectTerminal(out)
	if mode == PresentationRich {
		profile.TTY = true
		profile.Interactive = true
		if profile.Color == ColorUnknown || profile.Color == ColorNone {
			profile.Color = ColorBasic
		}
	}
	return NewPresenterWithProfile(out, mode, profile)
}

// NewPresenterWithCapabilities is the deterministic seam for callers and
// tests that know both the presentation mode and terminal capabilities.
func NewPresenterWithCapabilities(out io.Writer, mode PresentationMode, tty, color bool) Presenter {
	level := ColorNone
	if tty && color {
		level = ColorBasic
	}
	return NewPresenterWithProfile(out, mode, TerminalProfile{
		TTY:         tty,
		Interactive: tty,
		Color:       level,
		Unicode:     tty && color && detectUnicode(tty),
	})
}

// NewPresenterWithProfile is the explicit capability seam for callers and
// tests. A zero or unknown color level intentionally produces plain text.
func NewPresenterWithProfile(out io.Writer, mode PresentationMode, profile TerminalProfile) Presenter {
	if out == nil {
		out = io.Discard
	}
	if mode == PresentationRaw {
		return Presenter{Out: out, Mode: mode, Profile: profile}
	}
	return Presenter{
		Out:     out,
		TTY:     profile.TTY,
		Color:   mode == PresentationRich && profile.TTY && profile.Color.Enabled(),
		Mode:    mode,
		Profile: profile,
	}
}

func presentationMode(tty bool) PresentationMode {
	if tty {
		return PresentationRich
	}
	return PresentationPlain
}

func newPresenterWithPolicy(out io.Writer, tty, color, noColor bool) Presenter {
	return newPresenterWithPresentationPolicy(out, presentationMode(tty), tty, color, noColor)
}

func newPresenterWithPresentationPolicy(out io.Writer, mode PresentationMode, tty, color, noColor bool) Presenter {
	level := ColorNone
	if tty && color && !noColor {
		level = ColorBasic
	}
	return NewPresenterWithProfile(out, mode, TerminalProfile{
		TTY:         tty,
		Interactive: tty,
		Color:       level,
		Unicode:     tty && color && !noColor && detectUnicode(tty),
	})
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
	return DetectTerminalProfile(in, out).Interactive
}

// Style returns text decorated for its semantic role when terminal color is
// enabled. The original text is returned byte-for-byte otherwise.
func (p Presenter) Style(role Role, text string) string {
	if p.Mode == PresentationRaw || !p.Color || text == "" || role == RolePlain {
		return text
	}
	code := ""
	switch role {
	case RoleHeading:
		code = "\x1b[1m"
	case RoleLabel:
		code = "\x1b[2m"
	case RoleSuccess:
		code = "\x1b[32m"
	case RoleWarning:
		code = "\x1b[33m"
	case RoleError:
		code = "\x1b[31m"
	case RoleAccent:
		code = "\x1b[36m"
	case RoleMuted:
		code = "\x1b[2m"
	}
	if code == "" {
		return text
	}
	return code + text + ansiReset
}

func (p Presenter) richVisuals() bool {
	return p.Mode == PresentationRich && p.Color
}

func (p Presenter) glyph(unicode, ascii string) string {
	if p.Profile.Unicode {
		return unicode
	}
	return ascii
}

func (p Presenter) eventLine(unicodeGlyph, asciiGlyph, indent string, role Role, format string, args ...any) {
	if !p.richVisuals() {
		p.semanticLine(role, "==> "+format, args...)
		return
	}
	p.semanticLine(role, "%s%s ==> %s", indent, p.glyph(unicodeGlyph, asciiGlyph), fmt.Sprintf(format, args...))
}

// Hyperlink wraps visible text in an OSC-8 hyperlink only when the profile
// explicitly established support. Invalid control-containing URLs are left
// visible and unlinked.
func (p Presenter) Hyperlink(text, target string) string {
	if p.Mode == PresentationRaw || !p.Profile.Hyperlinks || text == "" || target == "" {
		return text
	}
	if strings.ContainsAny(target, "\x00\x1b\r\n") {
		return text
	}
	return "\x1b]8;;" + target + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// FileURL returns a local file URL suitable for an OSC-8 target.
func FileURL(path string) string {
	if path == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// GitSummary presents a compact, orchestration-owned repository summary.
// Paths are intentionally not listed here; callers can inspect Git state or
// request logs explicitly without turning normal presentation into a diff.
func (p Presenter) GitSummary(scope string, changedFiles []string) {
	if scope == "" {
		scope = "repository"
	}
	count := len(changedFiles)
	word := "files"
	if count == 1 {
		word = "file"
	}
	p.eventLine("└", "-", "  ", RoleMuted, "Git %s: %d %s changed", scope, count, word)
}

// Label formats a human-facing field label with its semantic label role.
func (p Presenter) Label(name string) string {
	return p.Style(RoleLabel, name+":")
}

// State formats a durable state value with the role appropriate to that
// state. Unknown states remain visible and use the accent role.
func (p Presenter) State(state string) string {
	return p.Style(StateRole(state), state)
}

// Line writes one formatted line with semantic styling.
func (p Presenter) Line(role Role, format string, args ...any) {
	fmt.Fprintln(p.Out, p.Style(role, fmt.Sprintf(format, args...)))
}

// Print writes formatted text without adding a newline.
func (p Presenter) Print(role Role, format string, args ...any) {
	fmt.Fprint(p.Out, p.Style(role, fmt.Sprintf(format, args...)))
}

// Raw writes an already-owned stream without adding or removing bytes. It is
// the explicit boundary for provider output, logs, JSON, YAML, and detached
// capture.
func (p Presenter) Raw(text string) {
	fmt.Fprint(p.Out, text)
}

// RawLine writes one already-owned line without styling or framing.
func (p Presenter) RawLine(format string, args ...any) {
	fmt.Fprintln(p.Out, fmt.Sprintf(format, args...))
}

func (p Presenter) semanticLine(role Role, format string, args ...any) {
	if p.Mode == PresentationRaw {
		return
	}
	p.Line(role, format, args...)
}

// TextLine writes human-facing text as a semantic event. Raw mode suppresses
// it; callers with an owned payload should use Raw instead.
func (p Presenter) TextLine(format string, args ...any) {
	p.semanticLine(RolePlain, format, args...)
}

// Prompt writes an interactive human prompt. Raw mode suppresses it along
// with other AgentFlow framing.
func (p Presenter) Prompt(format string, args ...any) {
	if p.Mode == PresentationRaw {
		return
	}
	p.Print(RoleAccent, format, args...)
}

// PhaseStart presents the beginning of a phase.
func (p Presenter) PhaseStart(id, label string) {
	p.eventLine("▸", ">", "", RoleAccent, "Phase %s: %s", id, label)
}

// PhaseSkip presents a condition-based phase skip.
func (p Presenter) PhaseSkip(id, reason string) {
	if reason == "" {
		p.eventLine("·", "-", "", RoleMuted, "Skipping phase %s", id)
		return
	}
	p.eventLine("·", "-", "", RoleMuted, "Skipping phase %s: %s", id, reason)
}

// CompletedPhaseSkip presents reuse of a completed phase marker.
func (p Presenter) CompletedPhaseSkip(id, label, commit string) {
	p.eventLine("·", "-", "", RoleMuted, "Skipping completed phase %s: %s (%s)", id, label, commit)
}

// CriterionAlreadyChecked presents the legacy checked-criterion shortcut.
func (p Presenter) CriterionAlreadyChecked(id string) {
	p.eventLine("✓", "+", "  ", RoleSuccess, "Criterion already checked; marking phase %s complete", id)
}

// PhaseResume presents recovery of an interrupted phase.
func (p Presenter) PhaseResume(id, label string) {
	p.eventLine("↺", "~", "", RoleAccent, "Recovering interrupted phase %s: %s", id, label)
}

// HumanGateAlreadyRecorded presents durable human-gate evidence reuse.
func (p Presenter) HumanGateAlreadyRecorded(id string) {
	p.eventLine("·", "-", "  ", RoleAccent, "Human gate %s already recorded", id)
}

// RetainedWorkResume presents recovery decisions for retained phase work.
func (p Presenter) RetainedWorkResume(actor string) {
	p.eventLine("↺", "~", "  ", RoleWarning, "Retained phase work is not yet acceptable; resuming actor %s", actor)
}

// RetainedWorkPreflight presents a successful preflight that still lacks actor
// completion evidence.
func (p Presenter) RetainedWorkPreflight() {
	p.eventLine(
		"·", "-", "  ", RoleWarning,
		"Retained phase work passed a preflight gate; actor completion evidence is still required",
	)
}

// ProviderIdentity presents the provider and actor owning an execution unit.
func (p Presenter) ProviderIdentity(provider, actor string) {
	if actor == "" {
		p.eventLine("·", "-", "  ", RoleAccent, "Provider %s", provider)
		return
	}
	p.eventLine("·", "-", "  ", RoleAccent, "Provider %s: actor %s", provider, actor)
}

// ValidationSuccess presents a successful deterministic validation.
func (p Presenter) ValidationSuccess(name string) {
	p.eventLine("✓", "+", "  ", RoleSuccess, "Validation %s passed", name)
}

// ValidationFailure presents a failed deterministic validation.
func (p Presenter) ValidationFailure(name string) {
	p.eventLine("✗", "!", "  ", RoleError, "Validation %s failed", name)
}

// ValidationReuse presents reuse of durable deterministic validation evidence.
func (p Presenter) ValidationReuse(name string) {
	p.eventLine("✓", "+", "  ", RoleSuccess, "Reusing deterministic validation evidence: %s", name)
}

// RepairAttempt presents the bounded repair transition after validation
// failure. Its plain text is retained for compatibility with existing output.
func (p Presenter) RepairAttempt(name string) {
	p.eventLine("↻", "~", "  ", RoleWarning, "Validation %s failed; running one repair attempt", name)
}

// CheckpointSummary presents a completed checkpoint when a commit is known.
func (p Presenter) CheckpointSummary(label, commit string) {
	if commit == "" {
		p.eventLine("✓", "+", "  ", RoleSuccess, "Checkpoint %s complete", label)
		return
	}
	p.eventLine("✓", "+", "  ", RoleSuccess, "Checkpoint %s complete at %s", label, commit)
}

// PhaseComplete presents a phase acceptance summary.
func (p Presenter) PhaseComplete(id, commit string) {
	p.eventLine("✓", "+", "  ", RoleSuccess, "Phase %s complete at %s", id, commit)
}

// CompletionSummary presents the final workflow completion notice.
func (p Presenter) CompletionSummary(name string) {
	p.eventLine("✓", "+", "", RoleSuccess, "Workflow %s complete.", name)
}

// WorkflowAlreadyComplete presents durable completion-marker reuse.
func (p Presenter) WorkflowAlreadyComplete(name, commit string) {
	p.eventLine("✓", "+", "", RoleSuccess, "Workflow %s already complete at %s", name, commit)
}

// Notice presents a human-facing notice with a semantic role.
func (p Presenter) Notice(role Role, format string, args ...any) {
	p.semanticLine(role, format, args...)
}

// KeyValue presents metadata using the renderer's stable label/value spacing.
func (p Presenter) KeyValue(key, value string) {
	p.Metadata(key, value)
}

// Metadata presents a stable human-readable key/value field.
func (p Presenter) Metadata(label, value string) {
	p.MetadataStyled(label, value, RolePlain)
}

// MetadataStyled presents a key/value field while styling its value
// semantically in Rich mode.
func (p Presenter) MetadataStyled(label, value string, role Role) {
	p.IndentedMetadata("", label, value, role)
}

// IndentedMetadata presents a key/value field with a renderer-owned prefix.
func (p Presenter) IndentedMetadata(indent, label, value string, role Role) {
	if label == "" {
		p.semanticLine(role, "%s%s", indent, value)
		return
	}
	if label[len(label)-1] != ':' {
		label += ":"
	}
	p.semanticLine(RolePlain, "%s%s %s", indent, p.Style(RoleLabel, label), p.Style(role, value))
}

// ListItem presents a single human-readable metadata item in a stable list.
func (p Presenter) ListItem(key, value string) {
	p.semanticLine(RolePlain, "- %s %s", p.Label(key), value)
}

// Rule presents a human-facing section rule.
func (p Presenter) Rule(label string) {
	if label == "" {
		p.semanticLine(RoleMuted, "--------------------")
		return
	}
	p.semanticLine(RoleMuted, "=== %s ===", label)
}

// Separator presents a blank separator in human-facing output.
func (p Presenter) Separator() {
	if p.Mode == PresentationRaw {
		return
	}
	fmt.Fprintln(p.Out)
}

// StateRole maps durable status values to a semantic role for human display.
func StateRole(state string) Role {
	switch state {
	case "ready", "completed", "running":
		return RoleSuccess
	case "human-gated", "validation-failed/recoverable", "validation", "not_running":
		return RoleWarning
	case "safety-failed/terminal", "safety", "malformed":
		return RoleError
	case "active":
		return RoleProgress
	default:
		return RoleAccent
	}
}
