package engine

import (
	"strings"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
)

func TestResolveParameters(t *testing.T) {
	t.Setenv("AGENTFLOW_PARAMETER_BOOL", "false")
	workflowWith := func(parameters map[string]workflow.Parameter) *workflow.Workflow {
		return &workflow.Workflow{Metadata: workflow.Metadata{Name: "parameters"}, Spec: workflow.Spec{Parameters: parameters}}
	}
	cases := []struct {
		name       string
		workflow   *workflow.Workflow
		overrides  map[string]string
		want       map[string]any
		errContain string
	}{
		{
			name: "typed defaults and environment",
			workflow: workflowWith(map[string]workflow.Parameter{
				"name":    {Type: "string", Default: "default"},
				"enabled": {Type: "boolean", Default: true, Env: "AGENTFLOW_PARAMETER_BOOL"},
				"limit":   {Type: "integer", Default: 3},
			}),
			want: map[string]any{"name": "default", "enabled": false, "limit": 3},
		},
		{
			name: "cli override wins over environment",
			workflow: workflowWith(map[string]workflow.Parameter{
				"enabled": {Type: "boolean", Default: true, Env: "AGENTFLOW_PARAMETER_BOOL"},
			}),
			overrides: map[string]string{"enabled": "true"},
			want:      map[string]any{"enabled": true},
		},
		{
			name: "defaults resolve forward parameter references",
			workflow: workflowWith(map[string]workflow.Parameter{
				"label": {Type: "string", Default: "{{ parameters.base }}-workflow"},
				"base":  {Type: "string", Default: "agentflow"},
			}),
			want: map[string]any{"base": "agentflow", "label": "agentflow-workflow"},
		},
		{
			name: "unknown override fails",
			workflow: workflowWith(map[string]workflow.Parameter{
				"enabled": {Type: "boolean", Default: true},
			}),
			overrides:  map[string]string{"missing": "true"},
			errContain: "unknown parameter override",
		},
		{
			name: "bad override type fails",
			workflow: workflowWith(map[string]workflow.Parameter{
				"limit": {Type: "integer", Default: 1},
			}),
			overrides:  map[string]string{"limit": "many"},
			errContain: "parameter limit: must be integer",
		},
		{
			name: "cyclic defaults fail",
			workflow: workflowWith(map[string]workflow.Parameter{
				"one": {Type: "string", Default: "{{ parameters.two }}"},
				"two": {Type: "string", Default: "{{ parameters.one }}"},
			}),
			errContain: "cyclic default reference",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveParameters(tc.workflow, tc.overrides)
			if tc.errContain != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errContain) {
					t.Fatalf("error = %v, want %q", err, tc.errContain)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("%s = %#v, want %#v", key, got[key], want)
				}
			}
		})
	}
}
