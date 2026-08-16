package workflow

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// Context is the complete, deliberately bounded expression environment. The
// evaluator never exposes Go values, filesystem access, commands, indexing, or
// arbitrary function calls to a workflow document.
type Context struct {
	Metadata      Metadata
	Parameters    map[string]any
	Paths         map[string]string
	State         map[string]any
	Phase         *Phase
	Progress      ProgressContext
	WorkflowFile  string
	FailureLog    string
	HeadCommit    string
	InvocationID  string
	TempDirectory string
}

// ProgressContext provides the small amount of progress information control
// flow needs. A workflow cannot inspect or transform arbitrary source text.
type ProgressContext struct {
	UncheckedCount int
	NextUnchecked  string
	IsChecked      func(string) (bool, error)
}

// Expand substitutes expressions into ordinary strings. Values are formatted
// only at this boundary; conditions and parameter defaults use EvalTemplate so
// their types are preserved.
func (c Context) Expand(s string) (string, error) {
	var out strings.Builder
	for rest := s; ; {
		open := strings.Index(rest, "{{")
		close := strings.Index(rest, "}}")
		if close >= 0 && (open < 0 || close < open) {
			return "", fmt.Errorf("unmatched closing delimiter")
		}
		if open < 0 {
			out.WriteString(rest)
			return out.String(), nil
		}
		out.WriteString(rest[:open])
		rest = rest[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return "", fmt.Errorf("missing closing delimiter")
		}
		v, err := c.Eval(strings.TrimSpace(rest[:end]))
		if err != nil {
			return "", err
		}
		if v == nil {
			return "", fmt.Errorf("expression %q has no value", strings.TrimSpace(rest[:end]))
		}
		out.WriteString(fmt.Sprint(v))
		rest = rest[end+2:]
	}
}

// EvalTemplate evaluates a template that consists of exactly one expression,
// preserving booleans and integers. It is used for typed fields.
func (c Context) EvalTemplate(s string) (any, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{{") {
		if !strings.HasSuffix(s, "}}") || strings.Count(s, "{{") != 1 || strings.Count(s, "}}") != 1 {
			return nil, fmt.Errorf("typed expression must contain exactly one complete template")
		}
		return c.Eval(strings.TrimSpace(s[2 : len(s)-2]))
	}
	if strings.Contains(s, "{{") || strings.Contains(s, "}}") {
		return nil, fmt.Errorf("typed expression must contain exactly one complete template")
	}
	return c.Eval(s)
}

func (c Context) Bool(expr string) (bool, error) {
	if strings.TrimSpace(expr) == "" {
		return true, nil
	}
	v, err := c.EvalTemplate(expr)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("expression %q is %s, not boolean", expr, valueType(v))
	}
	return b, nil
}

func (c Context) Int(expr string) (int, error) {
	v, err := c.EvalTemplate(expr)
	if err != nil {
		return 0, err
	}
	n, ok := v.(int)
	if !ok {
		return 0, fmt.Errorf("expression %q is %s, not integer", expr, valueType(v))
	}
	return n, nil
}

// Eval parses and evaluates the v1alpha1 expression grammar. The grammar is
// intentionally finite: literals, approved references, comparisons, boolean
// operators, default(...), progress.is_checked(...), and tail(...).
func (c Context) Eval(source string) (any, error) {
	expr, err := parseExpression(source)
	if err != nil {
		return nil, err
	}
	return expr.eval(c)
}

func valueType(v any) string {
	switch v.(type) {
	case nil:
		return "absent value"
	case bool:
		return "boolean"
	case int:
		return "integer"
	case string:
		return "string"
	default:
		return fmt.Sprintf("%T", v)
	}
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenIdentifier
	tokenString
	tokenInteger
	tokenLParen
	tokenRParen
	tokenComma
	tokenPipe
	tokenEqual
	tokenNotEqual
	tokenGreater
	tokenGreaterEqual
	tokenLess
	tokenLessEqual
)

type token struct {
	kind  tokenKind
	text  string
	value any
	pos   int
}

func lex(source string) ([]token, error) {
	var out []token
	for i := 0; i < len(source); {
		if unicode.IsSpace(rune(source[i])) {
			i++
			continue
		}
		start := i
		switch source[i] {
		case '(':
			out = append(out, token{kind: tokenLParen, text: "(", pos: i})
			i++
		case ')':
			out = append(out, token{kind: tokenRParen, text: ")", pos: i})
			i++
		case ',':
			out = append(out, token{kind: tokenComma, text: ",", pos: i})
			i++
		case '|':
			out = append(out, token{kind: tokenPipe, text: "|", pos: i})
			i++
		case '=', '!', '>', '<':
			ch := source[i]
			i++
			if i < len(source) && source[i] == '=' {
				i++
				kind := tokenEqual
				switch ch {
				case '!':
					kind = tokenNotEqual
				case '>':
					kind = tokenGreaterEqual
				case '<':
					kind = tokenLessEqual
				}
				out = append(out, token{kind: kind, text: source[start:i], pos: start})
			} else {
				if ch == '=' || ch == '!' {
					return nil, fmt.Errorf("unexpected %q at column %d", source[start:i], start+1)
				}
				kind := tokenGreater
				if ch == '<' {
					kind = tokenLess
				}
				out = append(out, token{kind: kind, text: source[start:i], pos: start})
			}
		case '\'', '"':
			quote := source[i]
			i++
			var b strings.Builder
			closed := false
			for i < len(source) {
				if source[i] == quote {
					i++
					closed = true
					break
				}
				if source[i] == '\\' {
					i++
					if i == len(source) {
						break
					}
					switch source[i] {
					case 'n':
						b.WriteByte('\n')
					case 'r':
						b.WriteByte('\r')
					case 't':
						b.WriteByte('\t')
					case '\\', '\'', '"':
						b.WriteByte(source[i])
					default:
						return nil, fmt.Errorf("unsupported escape at column %d", i)
					}
					i++
					continue
				}
				b.WriteByte(source[i])
				i++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string at column %d", start+1)
			}
			out = append(out, token{kind: tokenString, text: source[start:i], value: b.String(), pos: start})
		default:
			if source[i] >= '0' && source[i] <= '9' {
				for i < len(source) && source[i] >= '0' && source[i] <= '9' {
					i++
				}
				n, err := strconv.Atoi(source[start:i])
				if err != nil {
					return nil, fmt.Errorf("integer at column %d: %w", start+1, err)
				}
				out = append(out, token{kind: tokenInteger, text: source[start:i], value: n, pos: start})
				continue
			}
			if source[i] == '_' || unicode.IsLetter(rune(source[i])) {
				i++
				for i < len(source) && (source[i] == '_' || source[i] == '-' || source[i] == '.' || unicode.IsLetter(rune(source[i])) || unicode.IsDigit(rune(source[i]))) {
					i++
				}
				text := source[start:i]
				if strings.HasPrefix(text, ".") || strings.HasSuffix(text, ".") || strings.Contains(text, "..") {
					return nil, fmt.Errorf("invalid reference %q at column %d", text, start+1)
				}
				out = append(out, token{kind: tokenIdentifier, text: text, pos: start})
				continue
			}
			return nil, fmt.Errorf("unexpected character %q at column %d", source[i], i+1)
		}
	}
	return append(out, token{kind: tokenEOF, pos: len(source)}), nil
}

type expression interface {
	eval(Context) (any, error)
	validate(StaticContext) error
}
type literalExpression struct{ value any }

func (x literalExpression) eval(Context) (any, error)    { return x.value, nil }
func (x literalExpression) validate(StaticContext) error { return nil }

type referenceExpression struct{ name string }

func (x referenceExpression) eval(c Context) (any, error)    { return c.reference(x.name) }
func (x referenceExpression) validate(s StaticContext) error { return s.validateReference(x.name) }

type unaryExpression struct {
	op    string
	value expression
}

func (x unaryExpression) eval(c Context) (any, error) {
	v, err := x.value.eval(c)
	if err != nil {
		return nil, err
	}
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("not operand is %s, not boolean", valueType(v))
	}
	return !b, nil
}
func (x unaryExpression) validate(s StaticContext) error { return x.value.validate(s) }

type binaryExpression struct {
	left  expression
	op    string
	right expression
}

func (x binaryExpression) eval(c Context) (any, error) {
	l, err := x.left.eval(c)
	if err != nil {
		return nil, err
	}
	if x.op == "and" {
		b, ok := l.(bool)
		if !ok {
			return nil, fmt.Errorf("and left operand is %s, not boolean", valueType(l))
		}
		if !b {
			return false, nil
		}
		r, err := x.right.eval(c)
		if err != nil {
			return nil, err
		}
		rb, ok := r.(bool)
		if !ok {
			return nil, fmt.Errorf("and right operand is %s, not boolean", valueType(r))
		}
		return rb, nil
	}
	if x.op == "or" {
		b, ok := l.(bool)
		if !ok {
			return nil, fmt.Errorf("or left operand is %s, not boolean", valueType(l))
		}
		if b {
			return true, nil
		}
		r, err := x.right.eval(c)
		if err != nil {
			return nil, err
		}
		rb, ok := r.(bool)
		if !ok {
			return nil, fmt.Errorf("or right operand is %s, not boolean", valueType(r))
		}
		return rb, nil
	}
	r, err := x.right.eval(c)
	if err != nil {
		return nil, err
	}
	switch x.op {
	case "==", "!=":
		if l == nil || r == nil {
			if x.op == "==" {
				return l == nil && r == nil, nil
			}
			return l != nil || r != nil, nil
		}
		if valueType(l) != valueType(r) {
			return nil, fmt.Errorf("cannot compare %s and %s", valueType(l), valueType(r))
		}
		equal := l == r
		if x.op == "!=" {
			return !equal, nil
		}
		return equal, nil
	case ">", ">=", "<", "<=":
		li, lok := l.(int)
		ri, rok := r.(int)
		if !lok || !rok {
			return nil, fmt.Errorf("%s comparison requires integers, got %s and %s", x.op, valueType(l), valueType(r))
		}
		switch x.op {
		case ">":
			return li > ri, nil
		case ">=":
			return li >= ri, nil
		case "<":
			return li < ri, nil
		default:
			return li <= ri, nil
		}
	}
	return nil, fmt.Errorf("unsupported operator %q", x.op)
}
func (x binaryExpression) validate(s StaticContext) error {
	if err := x.left.validate(s); err != nil {
		return err
	}
	return x.right.validate(s)
}

type callExpression struct {
	name string
	args []expression
}

func (x callExpression) eval(c Context) (any, error) {
	switch x.name {
	case "mktemp":
		if len(x.args) != 1 {
			return nil, fmt.Errorf("mktemp requires exactly one argument")
		}
		value, err := x.args[0].eval(c)
		if err != nil {
			return nil, err
		}
		pattern, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("mktemp pattern is %s, not string", valueType(value))
		}
		return pattern, nil
	case "tail":
		if len(x.args) != 2 {
			return nil, fmt.Errorf("tail requires exactly two arguments")
		}
		value, err := x.args[0].eval(c)
		if err != nil {
			return nil, err
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("tail first argument is %s, not string", valueType(value))
		}
		n, err := x.args[1].eval(c)
		if err != nil {
			return nil, err
		}
		count, ok := n.(int)
		if !ok || count <= 0 {
			return nil, fmt.Errorf("tail count must be a positive integer")
		}
		return tailLines(text, count), nil
	case "progress.is_checked":
		if len(x.args) != 1 {
			return nil, fmt.Errorf("progress.is_checked requires exactly one argument")
		}
		value, err := x.args[0].eval(c)
		if err != nil {
			return nil, err
		}
		criterion, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("progress.is_checked argument is %s, not string", valueType(value))
		}
		if c.Progress.IsChecked == nil {
			return nil, fmt.Errorf("progress is unavailable")
		}
		return c.Progress.IsChecked(criterion)
	default:
		return nil, fmt.Errorf("unsupported expression function %q", x.name)
	}
}
func (x callExpression) validate(s StaticContext) error {
	if x.name != "tail" && x.name != "progress.is_checked" && x.name != "mktemp" {
		return fmt.Errorf("unsupported expression function %q", x.name)
	}
	for _, a := range x.args {
		if err := a.validate(s); err != nil {
			return err
		}
	}
	return nil
}

type defaultExpression struct{ value, fallback expression }

func (x defaultExpression) eval(c Context) (any, error) {
	value, err := x.value.eval(c)
	if err != nil {
		return nil, err
	}
	if value == nil || value == "" {
		return x.fallback.eval(c)
	}
	return value, nil
}
func (x defaultExpression) validate(s StaticContext) error {
	if err := x.value.validate(s); err != nil {
		return err
	}
	return x.fallback.validate(s)
}

type expressionParser struct {
	tokens []token
	at     int
}

func parseExpression(source string) (expression, error) {
	tokens, err := lex(strings.TrimSpace(source))
	if err != nil {
		return nil, err
	}
	p := expressionParser{tokens: tokens}
	x, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tokenPipe {
		p.next()
		filter := p.next()
		if filter.kind != tokenIdentifier || filter.text != "default" {
			return nil, fmt.Errorf("only default(...) filter is supported")
		}
		if !p.take(tokenLParen) {
			return nil, fmt.Errorf("default filter requires an argument")
		}
		fallback, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.take(tokenRParen) {
			return nil, fmt.Errorf("default filter requires a closing parenthesis")
		}
		x = defaultExpression{value: x, fallback: fallback}
	}
	if p.peek().kind != tokenEOF {
		return nil, fmt.Errorf("unexpected %q at column %d", p.peek().text, p.peek().pos+1)
	}
	return x, nil
}
func (p *expressionParser) peek() token { return p.tokens[p.at] }
func (p *expressionParser) next() token {
	t := p.peek()
	if t.kind != tokenEOF {
		p.at++
	}
	return t
}
func (p *expressionParser) take(k tokenKind) bool {
	if p.peek().kind != k {
		return false
	}
	p.next()
	return true
}
func (p *expressionParser) parseOr() (expression, error) {
	x, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokenIdentifier && p.peek().text == "or" {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		x = binaryExpression{left: x, op: "or", right: r}
	}
	return x, nil
}
func (p *expressionParser) parseAnd() (expression, error) {
	x, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tokenIdentifier && p.peek().text == "and" {
		p.next()
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		x = binaryExpression{left: x, op: "and", right: r}
	}
	return x, nil
}
func (p *expressionParser) parseUnary() (expression, error) {
	if p.peek().kind == tokenIdentifier && p.peek().text == "not" {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryExpression{op: "not", value: x}, nil
	}
	return p.parseComparison()
}
func (p *expressionParser) parseComparison() (expression, error) {
	x, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	ops := map[tokenKind]string{tokenEqual: "==", tokenNotEqual: "!=", tokenGreater: ">", tokenGreaterEqual: ">=", tokenLess: "<", tokenLessEqual: "<="}
	if op, ok := ops[p.peek().kind]; ok {
		p.next()
		r, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return binaryExpression{left: x, op: op, right: r}, nil
	}
	return x, nil
}
func (p *expressionParser) parsePrimary() (expression, error) {
	t := p.next()
	switch t.kind {
	case tokenString, tokenInteger:
		return literalExpression{value: t.value}, nil
	case tokenIdentifier:
		if t.text == "true" {
			return literalExpression{value: true}, nil
		}
		if t.text == "false" {
			return literalExpression{value: false}, nil
		}
		if p.take(tokenLParen) {
			var args []expression
			if !p.take(tokenRParen) {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.take(tokenRParen) {
						break
					}
					if !p.take(tokenComma) {
						return nil, fmt.Errorf("function %s requires a comma or closing parenthesis", t.text)
					}
				}
			}
			return callExpression{name: t.text, args: args}, nil
		}
		return referenceExpression{name: t.text}, nil
	case tokenLParen:
		x, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.take(tokenRParen) {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		return x, nil
	default:
		return nil, fmt.Errorf("expected expression at column %d", t.pos+1)
	}
}

func (c Context) reference(name string) (any, error) {
	switch name {
	case "metadata.name":
		return c.Metadata.Name, nil
	case "workflow.file":
		return c.WorkflowFile, nil
	case "head_commit":
		return c.HeadCommit, nil
	case "progress.unchecked_count":
		return c.Progress.UncheckedCount, nil
	case "progress.next_unchecked":
		return c.Progress.NextUnchecked, nil
	case "validation.failure.log":
		return c.FailureLog, nil
	case "invocation.id":
		if c.InvocationID == "" {
			return nil, fmt.Errorf("expression reference %q is unavailable in this runtime", name)
		}
		return c.InvocationID, nil
	case "temp.directory":
		if c.TempDirectory == "" {
			return nil, fmt.Errorf("expression reference %q is unavailable in this runtime", name)
		}
		return c.TempDirectory, nil
	}
	if strings.HasPrefix(name, "env.") {
		key := strings.TrimPrefix(name, "env.")
		if key == "" {
			return nil, fmt.Errorf("incomplete environment reference")
		}
		value, ok := os.LookupEnv(key)
		if !ok {
			return nil, nil
		}
		return value, nil
	}
	if strings.HasPrefix(name, "parameters.") {
		key := strings.TrimPrefix(name, "parameters.")
		value, ok := c.Parameters[key]
		if !ok {
			return nil, fmt.Errorf("unknown parameter %q", key)
		}
		return value, nil
	}
	if strings.HasPrefix(name, "spec.paths.") {
		key := strings.TrimPrefix(name, "spec.paths.")
		value, ok := c.Paths[key]
		if !ok {
			return nil, fmt.Errorf("unknown spec path %q", key)
		}
		return value, nil
	}
	if strings.HasPrefix(name, "state.") {
		return nestedReference(c.State, strings.Split(strings.TrimPrefix(name, "state."), "."), "state")
	}
	if strings.HasPrefix(name, "phase.") {
		if c.Phase == nil {
			return nil, fmt.Errorf("phase is unavailable")
		}
		switch strings.TrimPrefix(name, "phase.") {
		case "id":
			return c.Phase.ID, nil
		case "label":
			return c.Phase.Label, nil
		case "kind":
			return c.Phase.Kind, nil
		case "criterion":
			return c.Phase.Criterion, nil
		case "requiresChange":
			return c.Phase.RequiresChange, nil
		}
		return nil, fmt.Errorf("unknown phase reference %q", name)
	}
	return nil, fmt.Errorf("unsupported expression reference %q", name)
}
func nestedReference(values map[string]any, parts []string, root string) (any, error) {
	var value any = values
	for _, p := range parts {
		m, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unknown %s expression %q", root, strings.Join(parts, "."))
		}
		value, ok = m[p]
		if !ok {
			return nil, fmt.Errorf("unknown %s value %q", root, p)
		}
	}
	return value, nil
}

// StaticContext validates references that can be checked without a repository.
type StaticContext struct {
	Parameters map[string]Parameter
	Paths      map[string]string
}

func (s StaticContext) validateReference(name string) error {
	switch name {
	case "metadata.name", "workflow.file", "head_commit", "progress.unchecked_count", "progress.next_unchecked", "validation.failure.log", "invocation.id", "temp.directory":
		return nil
	}
	if strings.HasPrefix(name, "env.") {
		if validEnvironmentName(strings.TrimPrefix(name, "env.")) {
			return nil
		}
		return fmt.Errorf("invalid environment reference %q", name)
	}
	if strings.HasPrefix(name, "parameters.") {
		key := strings.TrimPrefix(name, "parameters.")
		if _, ok := s.Parameters[key]; !ok {
			return fmt.Errorf("unknown parameter reference %q", key)
		}
		return nil
	}
	if strings.HasPrefix(name, "spec.paths.") {
		key := strings.TrimPrefix(name, "spec.paths.")
		if _, ok := s.Paths[key]; !ok {
			return fmt.Errorf("unknown spec path reference %q", key)
		}
		return nil
	}
	if strings.HasPrefix(name, "phase.") {
		switch strings.TrimPrefix(name, "phase.") {
		case "id", "label", "kind", "criterion", "requiresChange":
			return nil
		}
		return fmt.Errorf("unknown phase reference %q", name)
	}
	if strings.HasPrefix(name, "state.") {
		switch strings.TrimPrefix(name, "state.") {
		case "initialized", "base_commit", "branch", "workflow_complete", "workflow_complete.exists", "workflow_complete.value", "active_phase", "active_phase.exists", "active_phase.value", "active_phase.phase_id", "active_phase.phase_start_commit", "active_phase.unchecked_count_before", "completed_phase_pattern", "completed_phases", "manual_confirmation", "human_verification":
			return nil
		}
		return fmt.Errorf("unknown state reference %q", name)
	}
	return fmt.Errorf("unsupported expression reference %q", name)
}
func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || unicode.IsLetter(r) || (i > 0 && unicode.IsDigit(r))) {
			return false
		}
	}
	return true
}

func parseTemplateExpressions(value string) ([]expression, error) {
	var out []expression
	for rest := value; ; {
		open := strings.Index(rest, "{{")
		close := strings.Index(rest, "}}")
		if close >= 0 && (open < 0 || close < open) {
			return nil, fmt.Errorf("unmatched closing delimiter")
		}
		if open < 0 {
			return out, nil
		}
		rest = rest[open+2:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			return nil, fmt.Errorf("missing closing delimiter")
		}
		x, err := parseExpression(strings.TrimSpace(rest[:end]))
		if err != nil {
			return nil, err
		}
		out = append(out, x)
		rest = rest[end+2:]
	}
}
func validateTemplate(value string, s StaticContext) error {
	expressions, err := parseTemplateExpressions(value)
	if err != nil {
		return err
	}
	for _, x := range expressions {
		if err := x.validate(s); err != nil {
			return err
		}
	}
	return nil
}

// validateTypedExpression is for fields whose whole value is an expression
// (conditions and loop bounds). Unlike ordinary string interpolation, literal
// surrounding text is not permitted.
func validateTypedExpression(value string, s StaticContext) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "{{") || strings.Contains(value, "}}") {
		if !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") || strings.Count(value, "{{") != 1 || strings.Count(value, "}}") != 1 {
			return fmt.Errorf("typed expression must contain exactly one complete template")
		}
		value = strings.TrimSpace(value[2 : len(value)-2])
	}
	x, err := parseExpression(value)
	if err != nil {
		return err
	}
	return x.validate(s)
}

// ParameterReferences returns only parsed parameter references in templates.
// It is used to resolve typed defaults deterministically without mistaking a
// string literal such as "parameters.example" for a dependency.
func ParameterReferences(value string) ([]string, error) {
	expressions, err := parseTemplateExpressions(value)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	var visit func(expression)
	visit = func(x expression) {
		switch value := x.(type) {
		case referenceExpression:
			if strings.HasPrefix(value.name, "parameters.") {
				name := strings.TrimPrefix(value.name, "parameters.")
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		case unaryExpression:
			visit(value.value)
		case binaryExpression:
			visit(value.left)
			visit(value.right)
		case callExpression:
			for _, argument := range value.args {
				visit(argument)
			}
		case defaultExpression:
			visit(value.value)
			visit(value.fallback)
		}
	}
	for _, x := range expressions {
		visit(x)
	}
	return out, nil
}
func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
