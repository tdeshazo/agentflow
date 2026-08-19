package engine

import (
	"context"
	"testing"

	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
)

type presentationRecordingProvider struct {
	request provider.Request
}

func (p *presentationRecordingProvider) Name() string { return "presentation-test" }

func (p *presentationRecordingProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	p.request = request
	return provider.Result{}, nil
}

func TestRunAgentPassesProviderNeutralPresentationIntent(t *testing.T) {
	for _, test := range []struct {
		name  string
		color string
		want  provider.PresentationIntent
	}{
		{name: "explicit always", color: "always", want: provider.PresentationAlways},
		{name: "omitted defaults to auto", want: provider.PresentationAuto},
		{name: "unknown defaults to auto", color: "unsupported", want: provider.PresentationAuto},
	} {
		t.Run(test.name, func(t *testing.T) {
			providerImpl := &presentationRecordingProvider{}
			e := &Engine{
				Workflow: &workflow.Workflow{
					Spec: workflow.Spec{
						Agents: map[string]workflow.Agent{
							"worker": {Runner: "test", Color: test.color},
						},
					},
				},
				Providers: map[string]provider.Provider{"test": providerImpl},
			}

			if err := e.runAgent(context.Background(), "worker", "high", "do work", nil); err != nil {
				t.Fatal(err)
			}
			if providerImpl.request.Presentation != test.want {
				t.Fatalf("presentation intent = %q, want %q", providerImpl.request.Presentation, test.want)
			}
		})
	}
}
