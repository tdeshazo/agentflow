package agentflowcli

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/workflow"
)

// workflowPickerInteractive is a seam for command tests. The production
// policy requires both input and output to be terminal-backed.
var workflowPickerInteractive = clioutput.IsInteractive

func pickWorkflow(repoRoot string, in io.Reader, out io.Writer, homeDir func() (string, error)) (string, error) {
	file, err := pickDiscoveredWorkflow(repoRoot, in, out, homeDir)
	if err != nil {
		return "", err
	}
	return file.Path, nil
}

// pickWorkflowSelector presents the common discovery picker and returns its
// logical name, so switch can persist a selector rather than a file path.
func pickWorkflowSelector(repoRoot string, in io.Reader, out io.Writer, homeDir func() (string, error)) (string, error) {
	file, err := pickDiscoveredWorkflow(repoRoot, in, out, homeDir)
	if err != nil {
		return "", err
	}
	return file.Name, nil
}

func pickDiscoveredWorkflow(repoRoot string, in io.Reader, out io.Writer, homeDir func() (string, error)) (workflow.DiscoveryFile, error) {
	discovery, err := workflow.DiscoverFiles(repoRoot, homeDir)
	if err != nil {
		return workflow.DiscoveryFile{}, err
	}
	path, err := selectWorkflow(discovery, in, clioutput.NewPresenter(out))
	if err != nil {
		return workflow.DiscoveryFile{}, err
	}
	for _, file := range discovery.Files {
		if file.Path == path {
			return file, nil
		}
	}
	return workflow.DiscoveryFile{}, fmt.Errorf("selected workflow %q was not discovered", path)
}

func selectWorkflow(discovery workflow.Discovery, in io.Reader, presenter clioutput.Presenter) (string, error) {
	if len(discovery.Files) == 0 {
		return "", fmt.Errorf("no workflows available; add a workflow under .agentflow/workflows/")
	}

	presenter.Line(clioutput.RoleHeading, "Select a workflow:")
	for i, file := range discovery.Files {
		presenter.Line(clioutput.RolePlain, "%d. %s (%s)", i+1, file.Name, file.Source)
	}
	presenter.Print(clioutput.RoleAccent, "Enter a number: ")

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 32), 64)
	if !scanner.Scan() {
		return "", fmt.Errorf("workflow selection failed: enter an integer from 1 to %d", len(discovery.Files))
	}
	line := scanner.Text()
	selection, parseErr := strconv.Atoi(strings.TrimSpace(line))
	if parseErr != nil || selection < 1 || selection > len(discovery.Files) {
		if parseErr == nil {
			return "", fmt.Errorf("workflow selection %d is out of range; enter an integer from 1 to %d", selection, len(discovery.Files))
		}
		return "", fmt.Errorf("invalid workflow selection %q; enter an integer from 1 to %d", strings.TrimSpace(line), len(discovery.Files))
	}
	return discovery.Files[selection-1].Path, nil
}

func missingWorkflowSelectorError(cmd string) error {
	return fmt.Errorf("-f workflow YAML is required when no selector is supplied; use -f workflow.yaml or agentflow %s workflow-name", cmd)
}

func missingWorkflowSwitchSelectorError() error {
	return fmt.Errorf("workflow selector is required when no selector is supplied; use agentflow switch workflow-name")
}
