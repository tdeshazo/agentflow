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
	providerImpl := &presentationRecordingProvider{}
	e := &Engine{
		Workflow: &workflow.Workflow{
			Spec: workflow.Spec{
				Agents: map[string]workflow.Agent{
					"worker": {Runner: "test", Color: "always"},
				},
			},
		},
		Providers: map[string]provider.Provider{"test": providerImpl},
	}

	if err := e.runAgent(context.Background(), "worker", "high", "do work", nil); err != nil {
		t.Fatal(err)
	}
	if providerImpl.request.Presentation != provider.PresentationAlways {
		t.Fatalf("presentation intent = %q, want %q", providerImpl.request.Presentation, provider.PresentationAlways)
	}
	if providerImpl.request.Color != "" {
		t.Fatalf("legacy provider color field was populated: %q", providerImpl.request.Color)
	}
}
