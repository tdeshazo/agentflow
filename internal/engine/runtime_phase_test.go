package engine

import (
	"context"
	"testing"

	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
)

type presentationRecordingProvider struct {
	request provider.Request
}

func (p *presentationRecordingProvider) Name() string { return "presentation-test" }

func (p *presentationRecordingProvider) Run(_ context.Context, request provider.Request) (provider.Result, error) {
	p.request = request
	return provider.Result{}, nil
}

func TestRunAgentUsesApplicationOwnedPresentationIntent(t *testing.T) {
	for _, test := range []struct {
		name     string
		detached bool
		want     provider.PresentationIntent
	}{
		{name: "attached uses automatic presentation", want: provider.PresentationAutomatic},
		{name: "detached is always plain", detached: true, want: provider.PresentationPlain},
	} {
		t.Run(test.name, func(t *testing.T) {
			providerImpl := &presentationRecordingProvider{}
			e := &Engine{
				Workflow: &workflow.Workflow{
					Spec: workflow.Spec{
						Agents: map[string]workflow.Agent{
							"worker": {Runner: "test"},
						},
					},
				},
				Providers: map[string]provider.Provider{"test": providerImpl},
				detached:  test.detached,
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
