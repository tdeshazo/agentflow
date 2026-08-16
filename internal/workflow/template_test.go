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
