package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/tdeshazo/agentflow-spec/internal/engine"
	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/observability"
	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
	codexprovider "github.com/tdeshazo/agentflow-spec/provider/codex"
)

type sets map[string]string

func (s *sets) String() string { return fmt.Sprint(map[string]string(*s)) }
func (s *sets) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("expected key=value")
	}
	if *s == nil {
		*s = sets{}
	}
	(*s)[k] = val
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentflow:", err)
		os.Exit(1)
	}
}

func run() error {
	return runArgs(os.Args[1:])
}

func runArgs(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	file := fs.String("f", "", "workflow YAML file")
	repo := fs.String("C", "", "repository root override")
	codexBin := fs.String("codex-bin", "codex", "Codex CLI binary")
	jsonOutput := fs.Bool("json", false, "emit machine-readable JSON (status only)")
	all := fs.Bool("all", false, "inspect every discovered workflow (status only)")
	workflowName := fs.String("workflow", "", "workflow name (logs only)")
	tail := fs.Int("tail", -1, "show the final N log lines (logs only)")
	follow := fs.Bool("follow", false, "follow appended workflow log output (logs only)")
	expanded := fs.Bool("expanded", false, "show resolved executable plan")
	var overrides sets
	fs.Var(&overrides, "set", "parameter override (key=value), repeatable")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	tailProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "tail" {
			tailProvided = true
		}
	})
	if *jsonOutput && cmd != "status" {
		return fmt.Errorf("--json is only supported with status")
	}
	if *all && cmd != "status" {
		return fmt.Errorf("--all is only supported with status")
	}
	if *workflowName != "" && cmd != "logs" {
		return fmt.Errorf("--workflow is only supported with logs")
	}
	if *tail != -1 && cmd != "logs" {
		return fmt.Errorf("--tail is only supported with logs")
	}
	if tailProvided && *tail < 0 {
		return fmt.Errorf("--tail must not be negative")
	}
	if *follow && cmd != "logs" {
		return fmt.Errorf("--follow is only supported with logs")
	}
	if cmd == "status" && *all && *file != "" {
		return fmt.Errorf("status selectors --all and -f are mutually exclusive")
	}
	if cmd == "logs" {
		if *file != "" {
			return fmt.Errorf("logs does not accept -f; use --workflow")
		}
		if *workflowName == "" {
			return fmt.Errorf("logs requires --workflow")
		}
		return runLogs(*repo, *workflowName, *tail, *follow)
	}
	if cmd == "status" && *all {
		return runAllStatus(*repo, *jsonOutput)
	}
	if *file == "" {
		return fmt.Errorf("-f workflow YAML is required")
	}
	result := workflow.ValidateFile(*file)
	if cmd == "validate" {
		for _, diagnostic := range result.Diagnostics {
			fmt.Fprintln(os.Stdout, diagnostic.String())
		}
		switch result.Status {
		case workflow.Executable:
			fmt.Fprintln(os.Stdout, "valid and executable")
			return nil
		case workflow.Unsupported:
			fmt.Fprintln(os.Stdout, "valid but unsupported by this runtime")
			return nil
		default:
			fmt.Fprintln(os.Stdout, "invalid")
			return fmt.Errorf("workflow is invalid")
		}
	}
	if result.Status == workflow.Invalid {
		return diagnosticsError(result)
	}
	if result.Status == workflow.Unsupported {
		return fmt.Errorf("workflow is valid but unsupported by this runtime: %s", diagnosticsError(result))
	}
	if cmd == "plan" {
		if !*expanded {
			return fmt.Errorf("plan requires --expanded")
		}
		// The flag package has already parsed flags at this point; keep this
		// branch below validation so plan never opens a repository.
		plan, err := workflow.BuildExpandedPlan(result.Document)
		if err != nil {
			return err
		}
		out, err := yaml.Marshal(plan)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	}
	w := result.Document.Workflow
	providers := map[string]provider.Provider{"codex": codexprovider.Provider{Binary: *codexBin}}
	stateOnly := cmd == "status" || cmd == "reset"
	e, err := engine.New(w, providers, engine.Options{RepoRoot: *repo, Overrides: map[string]string(overrides), StateOnly: stateOnly})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch cmd {
	case "run":
		return e.Run(ctx)
	case "status":
		if *jsonOutput {
			return e.StatusJSON()
		}
		return e.Status()
	case "reset":
		return e.Reset()
	default:
		return usage()
	}
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: agentflow <validate|plan|run|status|reset> -f workflow.yaml [-C repo] [--expanded] [--json] [--set key=value]")
	fmt.Fprintln(os.Stderr, "       agentflow status --all [-C repo] [--json]")
	fmt.Fprintln(os.Stderr, "       agentflow logs --workflow name [-C repo] [--tail n|--follow]")
	return fmt.Errorf("invalid command")
}

type statusAllOutput struct {
	SchemaVersion int                         `json:"schema_version"`
	Repo          string                      `json:"repo"`
	Workflows     []gitstate.StatusProjection `json:"workflows"`
}

func runAllStatus(repoRoot string, jsonOutput bool) error {
	repo, err := targetRepo(repoRoot)
	if err != nil {
		return err
	}
	items, err := repo.DiscoverDescriptors()
	if err != nil {
		return err
	}
	statuses := make([]gitstate.StatusProjection, 0, len(items))
	for _, item := range items {
		if item.Error != "" || item.Descriptor == nil {
			status := gitstate.StatusProjection{SchemaVersion: 1, Namespace: item.Namespace, Workflow: item.Workflow, Repo: repo.Root, State: "malformed", Error: item.Error}
			statuses = append(statuses, status)
			continue
		}
		status, projectErr := item.Descriptor.ProjectStatus(repo, item.Namespace)
		if projectErr != nil {
			statuses = append(statuses, gitstate.StatusProjection{SchemaVersion: 1, Namespace: item.Namespace, Workflow: item.Workflow, Repo: repo.Root, State: "malformed", Error: projectErr.Error()})
			continue
		}
		if liveness, verified := gitstate.ProcessLiveness(item.Descriptor.Process); verified {
			status.ProcessLiveness = liveness
		}
		statuses = append(statuses, status)
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(statusAllOutput{SchemaVersion: 1, Repo: repo.Root, Workflows: statuses})
	}
	fmt.Fprintf(os.Stdout, "repository: %s\nworkflows: %d\n", repo.Root, len(statuses))
	for _, status := range statuses {
		fmt.Fprintf(os.Stdout, "- workflow: %s\n  state: %s\n", status.Workflow, status.State)
		if status.Namespace != "" {
			fmt.Fprintf(os.Stdout, "  namespace: %s\n", status.Namespace)
		}
		if status.Error != "" {
			fmt.Fprintf(os.Stdout, "  error: %s\n", status.Error)
			continue
		}
		if status.Initialized {
			fmt.Fprintf(os.Stdout, "  base: %s\n  branch: %s\n  head: %s\n", status.Base, status.Branch, status.Head)
		}
		if status.ActivePhase != "" {
			fmt.Fprintf(os.Stdout, "  active_phase: %s\n", status.ActivePhase)
		}
		fmt.Fprintf(os.Stdout, "  complete: %v\n", status.Complete)
		if status.ProcessLiveness != "" {
			fmt.Fprintf(os.Stdout, "  process_liveness: %s\n", status.ProcessLiveness)
		}
	}
	return nil
}

func targetRepo(root string) (gitstate.Repo, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return gitstate.Repo{}, err
	}
	repo := gitstate.Repo{Root: abs}
	if !repo.IsRepository() {
		return gitstate.Repo{}, fmt.Errorf("%s is not a Git repository", abs)
	}
	return repo, nil
}

func runLogs(repoRoot, workflowName string, tail int, follow bool) error {
	repo, err := targetRepo(repoRoot)
	if err != nil {
		return err
	}
	discovery, found, err := repo.FindDescriptor(workflowName)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("unknown workflow %q", workflowName)
	}
	if discovery.Error != "" || discovery.Descriptor == nil {
		return fmt.Errorf("workflow %q has malformed observability metadata: %s", workflowName, discovery.Error)
	}
	data, path, readErr := observability.Read(repo, workflowName)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return fmt.Errorf("no logs for workflow %q", workflowName)
		}
		return readErr
	}
	if follow {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return observability.Follow(ctx, path, os.Stdout)
	}
	if tail >= 0 {
		data, err = observability.Tail(data, tail)
		if err != nil {
			return err
		}
	}
	_, err = os.Stdout.Write(data)
	return err
}

func diagnosticsError(result workflow.Result) error {
	if len(result.Diagnostics) == 0 {
		return fmt.Errorf("workflow is %s", result.Status)
	}
	parts := make([]string, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		parts = append(parts, d.String())
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}
