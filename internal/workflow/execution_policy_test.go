package workflow

import (
	"strings"
	"testing"
)

func TestEffectiveExecutionPolicyOnlyNarrowsAuthority(t *testing.T) {
	base := ExecutionPolicy{
		Network: "allow", Capabilities: []string{"deploy", "issues"},
		Credentials:  []CredentialScope{{Name: "release", Env: "RELEASE_TOKEN"}},
		ApprovalGate: "review",
		Budgets:      ResourceBudgets{ModelCalls: 4, Tokens: 1000, Duration: "10m"},
	}
	local := &ExecutionPolicy{
		Network: "deny", Capabilities: []string{"issues"}, Credentials: []CredentialScope{},
		Budgets: ResourceBudgets{ModelCalls: 2, Tokens: 500, Duration: "5m"},
	}
	got, err := EffectiveExecutionPolicy(base, local)
	if err != nil {
		t.Fatal(err)
	}
	if got.Network != "deny" || len(got.Capabilities) != 1 || len(got.Credentials) != 0 || got.ApprovalGate != "review" || got.Budgets.ModelCalls != 2 {
		t.Fatalf("effective policy = %#v", got)
	}

	for _, test := range []struct {
		name  string
		base  ExecutionPolicy
		local ExecutionPolicy
		want  string
	}{
		{name: "network", base: ExecutionPolicy{Network: "deny"}, local: ExecutionPolicy{Network: "allow"}, want: "broadens"},
		{name: "capability", base: ExecutionPolicy{}, local: ExecutionPolicy{Capabilities: []string{"deploy"}}, want: "not granted"},
		{name: "credential", base: ExecutionPolicy{}, local: ExecutionPolicy{Credentials: []CredentialScope{{Name: "x", Env: "TOKEN"}}}, want: "not granted"},
		{name: "budget", base: ExecutionPolicy{Budgets: ResourceBudgets{ModelCalls: 1}}, local: ExecutionPolicy{Budgets: ResourceBudgets{ModelCalls: 2}}, want: "exceeds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := EffectiveExecutionPolicy(test.base, &test.local)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateExecutionPolicyRequiresApprovalForPrivilegedEffects(t *testing.T) {
	w := &Workflow{Spec: Spec{
		Execution: ExecutionSpec{Policy: ExecutionPolicy{Network: "allow"}},
		Agents:    map[string]Agent{"worker": {}},
	}}
	if err := ValidateExecutionPolicy(w); err == nil || !strings.Contains(err.Error(), "approvalGate") {
		t.Fatalf("error = %v", err)
	}
	w.Spec.Execution.Policy.ApprovalGate = "review"
	w.Spec.HumanGates = []HumanGate{{ID: "review"}}
	if err := ValidateExecutionPolicy(w); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExecutionPolicyRequiresDedicatedPreauthorizationGate(t *testing.T) {
	for _, gate := range []HumanGate{
		{ID: "review", Requires: []string{"build"}},
		{ID: "review", If: "true"},
		{ID: "review", Evidence: Marker{Record: "custom"}},
	} {
		w := &Workflow{Spec: Spec{
			Execution:  ExecutionSpec{Policy: ExecutionPolicy{Network: "allow", ApprovalGate: "review"}},
			HumanGates: []HumanGate{gate},
		}}
		if err := ValidateExecutionPolicy(w); err == nil {
			t.Fatalf("approval gate unexpectedly valid: %#v", gate)
		}
	}
}

func TestValidateExecutionPolicyRejectsMalformedBudgetsAndCredentials(t *testing.T) {
	for _, policy := range []ExecutionPolicy{
		{Budgets: ResourceBudgets{Duration: "forever"}},
		{Budgets: ResourceBudgets{Tokens: -1}},
		{Credentials: []CredentialScope{{Name: "token", Env: "not-valid"}}, ApprovalGate: "review"},
	} {
		w := &Workflow{Spec: Spec{Execution: ExecutionSpec{Policy: policy}, HumanGates: []HumanGate{{ID: "review"}}}}
		if err := ValidateExecutionPolicy(w); err == nil {
			t.Fatalf("policy unexpectedly valid: %#v", policy)
		}
	}
}
