package workflow

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func Load(path string) (*Workflow, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var w Workflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("decode workflow: %w", err)
	}
	w.File, _ = filepath.Abs(path)
	if w.APIVersion != "agentflow.dev/v1alpha1" {
		return nil, fmt.Errorf("unsupported apiVersion %q", w.APIVersion)
	}
	if w.Kind != "AgentWorkflow" {
		return nil, fmt.Errorf("unsupported kind %q", w.Kind)
	}
	if w.Metadata.Name == "" {
		return nil, fmt.Errorf("metadata.name is required")
	}
	return &w, nil
}
