package provider

import "testing"

func TestResolvePresentationIntentIgnoresWorkflowValues(t *testing.T) {
	for _, value := range []string{"", "auto", "always", "never", "rich", "plain", "unexpected"} {
		if got := ResolvePresentationIntent(value); got != PresentationAutomatic {
			t.Errorf("ResolvePresentationIntent(%q) = %q, want %q", value, got, PresentationAutomatic)
		}
	}
}
