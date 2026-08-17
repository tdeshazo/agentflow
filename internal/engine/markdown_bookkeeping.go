package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

var markdownChecklistLine = regexp.MustCompile(`^(\s*(?:[-+*]|\d+[.)])\s+\[)( |x|X)(\]\s+)(.*)$`)

func (e *Engine) bookkeepingStateDigests(p *workflow.Phase) (map[string][]string, error) {
	states := map[string][]string{}
	contents := map[string][]byte{}
	for _, transition := range p.Bookkeeping {
		path, err := e.contextWithoutProgress(p).Expand(transition.Path)
		if err != nil {
			return nil, err
		}
		if _, ok := contents[path]; !ok {
			abs, err := e.markdownWorkspacePath(path)
			if err != nil {
				return nil, err
			}
			b, err := os.ReadFile(abs)
			if err != nil {
				return nil, err
			}
			contents[path] = b
			states[path] = []string{digestBytes(b)}
		}
		updated, changed, err := transitionMarkdownBytes(contents[path], transition)
		if err != nil {
			return nil, err
		}
		if !changed {
			return nil, fmt.Errorf("bookkeeping target in %s is already in the declared final state", path)
		}
		contents[path] = updated
		states[path] = append(states[path], digestBytes(updated))
	}
	return states, nil
}

func (e *Engine) assertBookkeepingState(p *workflow.Phase, active ActivePhase) error {
	if len(p.Bookkeeping) == 0 {
		return nil
	}
	if len(active.BookkeepingStateDigests) == 0 {
		return fmt.Errorf("bookkeeping phase %s is missing its durable file-state baseline", p.ID)
	}
	checked := map[string]bool{}
	for _, transition := range p.Bookkeeping {
		path, err := e.contextWithoutProgress(p).Expand(transition.Path)
		if err != nil {
			return err
		}
		if checked[path] {
			continue
		}
		checked[path] = true
		abs, err := e.markdownWorkspacePath(path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		want := digestBytes(b)
		allowed := false
		for _, digest := range active.BookkeepingStateDigests[path] {
			if digest == want {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("bookkeeping file %s changed outside its declared transitions", path)
		}
	}
	return nil
}

// replaceMarkdownChecklist changes exactly one semantic Markdown checklist
// item. It does not parse and re-render the document: every byte outside the
// checkbox marker is retained from the source read immediately before writing.
func (e *Engine) replaceMarkdownChecklist(p *workflow.Phase, configuredPath, item, state string) error {
	path, err := e.contextWithoutProgress(p).Expand(configuredPath)
	if err != nil {
		return err
	}
	return e.mutateMarkdownChecklist(path, item, state)
}

func (e *Engine) mutateMarkdownChecklist(path, item, state string) error {
	if state != "checked" && state != "unchecked" {
		return fmt.Errorf("invalid checklist state %q", state)
	}
	abs, err := e.markdownWorkspacePath(path)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(b), "\n")
	matches := 0
	replacement := " "
	if state == "checked" {
		replacement = "x"
	}
	for i, line := range lines {
		body, ending := splitMarkdownLineEnding(line)
		parts := markdownChecklistLine.FindStringSubmatch(body)
		if len(parts) == 0 || parts[4] != item {
			continue
		}
		matches++
		if parts[2] == replacement || (replacement == "x" && strings.EqualFold(parts[2], "x")) {
			continue
		}
		lines[i] = parts[1] + replacement + parts[3] + parts[4] + ending
	}
	if matches != 1 {
		return fmt.Errorf("Markdown checklist target %q in %s matched %d items", item, path, matches)
	}
	updated := strings.Join(lines, "")
	if updated == string(b) {
		return nil
	}
	return os.WriteFile(abs, []byte(updated), 0o644)
}

func (e *Engine) markdownWorkspacePath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("Markdown bookkeeping path %q must be relative to the workspace", path)
	}
	abs := filepath.Clean(filepath.Join(e.Repo.Root, path))
	rel, err := filepath.Rel(e.Repo.Root, abs)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Markdown bookkeeping path %q escapes the workspace", path)
	}
	return abs, nil
}

func splitMarkdownLineEnding(line string) (string, string) {
	if strings.HasSuffix(line, "\r\n") {
		return strings.TrimSuffix(line, "\r\n"), "\r\n"
	}
	if strings.HasSuffix(line, "\n") {
		return strings.TrimSuffix(line, "\n"), "\n"
	}
	return line, ""
}

func (e *Engine) applyBookkeeping(p *workflow.Phase, active *ActivePhase) error {
	if active.BookkeepingApplied {
		return e.assertBookkeepingSatisfied(p)
	}
	if active.BookkeepingPending {
		if err := e.applyMarkdownTransitions(p, true); err != nil {
			return err
		}
	} else {
		active.BookkeepingPending = true
		if err := e.Store.SetJSON(e.activeRecord(), *active); err != nil {
			return err
		}
		if err := e.applyMarkdownTransitions(p, false); err != nil {
			return err
		}
	}
	if err := e.assertBookkeepingSatisfied(p); err != nil {
		return err
	}
	active.BookkeepingPending = false
	active.BookkeepingApplied = true
	return e.Store.SetJSON(e.activeRecord(), *active)
}

// allowSatisfied permits recovery to replay a partially applied, durable
// bookkeeping transition. On a first attempt, an already-satisfied target is
// rejected so external edits cannot be claimed as engine authority.
func (e *Engine) applyMarkdownTransitions(p *workflow.Phase, allowSatisfied bool) error {
	for _, transition := range p.Bookkeeping {
		path, err := e.contextWithoutProgress(p).Expand(transition.Path)
		if err != nil {
			return err
		}
		var changed bool
		switch transition.Type {
		case "markdown-checklist", "markdown-index":
			changed, err = e.transitionMarkdownChecklist(path, transition.Item, transition.State)
		case "markdown-status":
			changed, err = e.transitionMarkdownStatus(path, transition.Label, transition.From, transition.To)
		default:
			return fmt.Errorf("unsupported Markdown transition type %q", transition.Type)
		}
		if err != nil {
			return err
		}
		if !changed && !allowSatisfied {
			return fmt.Errorf("bookkeeping target in %s is already in the declared final state", path)
		}
	}
	return nil
}

func (e *Engine) transitionMarkdownChecklist(path, item, state string) (bool, error) {
	abs, err := e.markdownWorkspacePath(path)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return false, err
	}
	updated, changed, err := transitionMarkdownChecklistBytes(b, item, state)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(abs, updated, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func transitionMarkdownChecklistBytes(b []byte, item, state string) ([]byte, bool, error) {
	if state != "checked" && state != "unchecked" {
		return nil, false, fmt.Errorf("invalid checklist state %q", state)
	}
	lines := strings.SplitAfter(string(b), "\n")
	matches := 0
	changed := false
	replacement := " "
	if state == "checked" {
		replacement = "x"
	}
	for i, line := range lines {
		body, ending := splitMarkdownLineEnding(line)
		parts := markdownChecklistLine.FindStringSubmatch(body)
		if len(parts) == 0 || parts[4] != item {
			continue
		}
		matches++
		if parts[2] == replacement || (replacement == "x" && strings.EqualFold(parts[2], "x")) {
			continue
		}
		lines[i] = parts[1] + replacement + parts[3] + parts[4] + ending
		changed = true
	}
	if matches != 1 {
		return nil, false, fmt.Errorf("Markdown checklist target %q matched %d items", item, matches)
	}
	return []byte(strings.Join(lines, "")), changed, nil
}

func (e *Engine) transitionMarkdownStatus(path, label, from, to string) (bool, error) {
	abs, err := e.markdownWorkspacePath(path)
	if err != nil {
		return false, err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return false, err
	}
	updated, changed, err := transitionMarkdownStatusBytes(b, label, from, to)
	if err != nil {
		return false, err
	}
	if !changed {
		return false, nil
	}
	if err := os.WriteFile(abs, updated, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func transitionMarkdownStatusBytes(b []byte, label, from, to string) ([]byte, bool, error) {
	lines := strings.SplitAfter(string(b), "\n")
	matches := 0
	changed := false
	for i, line := range lines {
		body, ending := splitMarkdownLineEnding(line)
		colon := strings.IndexByte(body, ':')
		if colon < 0 || strings.TrimSpace(body[:colon]) != label {
			continue
		}
		matches++
		valueStart := colon + 1
		for valueStart < len(body) && (body[valueStart] == ' ' || body[valueStart] == '\t') {
			valueStart++
		}
		valueEnd := len(body)
		for valueEnd > valueStart && (body[valueEnd-1] == ' ' || body[valueEnd-1] == '\t') {
			valueEnd--
		}
		value := body[valueStart:valueEnd]
		switch value {
		case to:
			continue
		case from:
			lines[i] = body[:valueStart] + to + body[valueEnd:] + ending
			changed = true
		default:
			return nil, false, fmt.Errorf("Markdown status %q is %q, want %q", label, value, from)
		}
	}
	if matches != 1 {
		return nil, false, fmt.Errorf("Markdown status target %q matched %d lines", label, matches)
	}
	return []byte(strings.Join(lines, "")), changed, nil
}

func transitionMarkdownBytes(b []byte, transition workflow.MarkdownTransition) ([]byte, bool, error) {
	switch transition.Type {
	case "markdown-checklist", "markdown-index":
		return transitionMarkdownChecklistBytes(b, transition.Item, transition.State)
	case "markdown-status":
		return transitionMarkdownStatusBytes(b, transition.Label, transition.From, transition.To)
	default:
		return nil, false, fmt.Errorf("unsupported Markdown transition type %q", transition.Type)
	}
}

func (e *Engine) assertBookkeepingSatisfied(p *workflow.Phase) error {
	for _, transition := range p.Bookkeeping {
		path, err := e.contextWithoutProgress(p).Expand(transition.Path)
		if err != nil {
			return err
		}
		switch transition.Type {
		case "markdown-checklist", "markdown-index":
			if err := e.assertMarkdownChecklistState(path, transition.Item, transition.State); err != nil {
				return err
			}
		case "markdown-status":
			if err := e.assertMarkdownStatusState(path, transition.Label, transition.To); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) assertMarkdownChecklistState(path, item, state string) error {
	abs, err := e.markdownWorkspacePath(path)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	want := " "
	if state == "checked" {
		want = "x"
	}
	matches := 0
	for _, line := range strings.Split(string(b), "\n") {
		parts := markdownChecklistLine.FindStringSubmatch(line)
		if len(parts) == 0 || parts[4] != item {
			continue
		}
		matches++
		if parts[2] != want && !(want == "x" && strings.EqualFold(parts[2], "x")) {
			return fmt.Errorf("Markdown checklist target %q in %s is not %s", item, path, state)
		}
	}
	if matches != 1 {
		return fmt.Errorf("Markdown checklist target %q in %s matched %d items", item, path, matches)
	}
	return nil
}

func (e *Engine) assertMarkdownStatusState(path, label, want string) error {
	abs, err := e.markdownWorkspacePath(path)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	matches := 0
	for _, line := range strings.Split(string(b), "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 || strings.TrimSpace(line[:colon]) != label {
			continue
		}
		matches++
		if strings.TrimSpace(line[colon+1:]) != want {
			return fmt.Errorf("Markdown status %q in %s is not %q", label, path, want)
		}
	}
	if matches != 1 {
		return fmt.Errorf("Markdown status target %q in %s matched %d lines", label, path, matches)
	}
	return nil
}
