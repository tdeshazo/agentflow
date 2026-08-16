package workflow

import "testing"

func TestExpand(t *testing.T) {
	c := Context{Metadata: Metadata{Name: "demo"}, Parameters: map[string]any{"model": "x"}, Paths: map[string]string{"gate": "scripts/check.sh"}}
	got, err := c.Expand("{{ metadata.name }} {{ parameters.model }} {{ spec.paths.gate }}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "demo x scripts/check.sh" {
		t.Fatalf("got %q", got)
	}
}

func TestUnsupportedExpressionFails(t *testing.T) {
	_, err := (Context{}).Expand("{{ magic.foo }}")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExpressionSemantics(t *testing.T) {
	c := Context{
		Parameters: map[string]any{"enabled": true, "count": 2, "name": "agentflow"},
		Phase:      &Phase{Kind: "criterion", RequiresChange: true},
		Progress: ProgressContext{UncheckedCount: 2, NextUnchecked: "first", IsChecked: func(text string) (bool, error) {
			return text == "first", nil
		}},
	}
	cases := []struct {
		name string
		expr string
		want any
	}{
		{name: "boolean precedence", expr: "{{ parameters.enabled and parameters.count > 1 }}", want: true},
		{name: "not and equality", expr: "{{ not phase.requiresChange or phase.kind == 'criterion' }}", want: true},
		{name: "progress function", expr: "{{ progress.is_checked(progress.next_unchecked) }}", want: true},
		{name: "integer value preserved", expr: "{{ progress.unchecked_count }}", want: 2},
		{name: "environment default", expr: "{{ env.AGENTFLOW_TEST_MISSING | default('fallback') }}", want: "fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.EvalTemplate(tc.expr)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

func TestExpressionFailuresAreClosed(t *testing.T) {
	cases := []string{
		"{{ parameters.missing }}",
		"{{ parameters.enabled == 'true' }}",
		"{{ unknown_function() }}",
		"{{ parameters.enabled | nope('x') }}",
		"{{ parameters.enabled and 'not a boolean' }}",
		"{{ parameters.enabled",
	}
	for _, expr := range cases {
		t.Run(expr, func(t *testing.T) {
			_, err := (Context{Parameters: map[string]any{"enabled": true}}).EvalTemplate(expr)
			if err == nil {
				t.Fatal("expected expression failure")
			}
		})
	}
}
