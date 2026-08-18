package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/tdeshazo/agentflow-spec/internal/clioutput"
	"github.com/tdeshazo/agentflow-spec/internal/engine"
	"github.com/tdeshazo/agentflow-spec/internal/gitstate"
	"github.com/tdeshazo/agentflow-spec/internal/observability"
	"github.com/tdeshazo/agentflow-spec/internal/workflow"
	"github.com/tdeshazo/agentflow-spec/provider"
	codexprovider "github.com/tdeshazo/agentflow-spec/provider/codex"
)

type sets struct {
	values []string
	parsed map[string]string
}

func (s *sets) String() string { return fmt.Sprint(s.parsed) }
func (s *sets) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" {
		return fmt.Errorf("expected key=value")
	}
	if s.parsed == nil {
		s.parsed = map[string]string{}
	}
	s.values = append(s.values, v)
	s.parsed[k] = val
	return nil
}

func (s sets) Map() map[string]string {
	return s.parsed
}

func (s sets) Values() []string {
	return append([]string(nil), s.values...)
}

const detachedChildEnv = "AGENTFLOW_DETACHED_CHILD"

var statusOutputIsTTY = clioutput.IsTTY

func main() {
	if err := run(); err != nil {
		presenter := clioutput.NewPresenter(os.Stderr)
		fmt.Fprintln(os.Stderr, presenter.Style(clioutput.RoleError, "agentflow:"), err)
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
	detach := fs.Bool("detach", false, "start the workflow in a detached child process (run only)")
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
	if *follow && tailProvided {
		return fmt.Errorf("--tail and --follow are mutually exclusive")
	}
	if *follow && cmd != "logs" {
		return fmt.Errorf("--follow is only supported with logs")
	}
	if *detach && cmd != "run" {
		return fmt.Errorf("%s does not support --detach; use --detach with run", cmd)
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
		presenter := clioutput.NewPresenter(os.Stdout)
		for _, diagnostic := range result.Diagnostics {
			role := clioutput.RoleWarning
			if result.Status == workflow.Invalid {
				role = clioutput.RoleError
			}
			presenter.Line(role, "%s", diagnostic.String())
		}
		switch result.Status {
		case workflow.Executable:
			presenter.Line(clioutput.RoleSuccess, "valid and executable")
			return nil
		case workflow.Unsupported:
			presenter.Line(clioutput.RoleWarning, "valid but unsupported by this runtime")
			return nil
		default:
			presenter.Line(clioutput.RoleError, "invalid")
			return fmt.Errorf("workflow is invalid")
		}
	}
	if result.Status == workflow.Invalid {
		return diagnosticsError(result)
	}
	if result.Status == workflow.Unsupported {
		return fmt.Errorf("workflow is valid but unsupported by this runtime: %s", diagnosticsError(result))
	}
	if *detach {
		pid, err := launchDetachedRun(os.Args[0], *file, *repo, *codexBin, overrides.Values(), result.Document.Workflow.Metadata.Name)
		if err != nil {
			return err
		}
		clioutput.NewPresenter(os.Stdout).Line(clioutput.RoleSuccess, "detached workflow %q started (pid %d)", result.Document.Workflow.Metadata.Name, pid)
		return nil
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
	e, err := engine.New(w, providers, engine.Options{RepoRoot: *repo, Overrides: overrides.Map(), StateOnly: stateOnly, Detached: os.Getenv(detachedChildEnv) == "1" && cmd == "run"})
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
			stdout := os.Stdout
			return e.StatusJSONTo(stdout, statusOutputIsTTY(stdout))
		}
		return e.Status()
	case "reset":
		return e.Reset()
	default:
		return usage()
	}
}

func usage() error {
	presenter := clioutput.NewPresenter(os.Stderr)
	fmt.Fprintf(os.Stderr, "%s agentflow <validate|plan|run|status|reset> -f workflow.yaml [-C repo] [--expanded] [--json] [--set key=value]\n", presenter.Style(clioutput.RoleLabel, "usage:"))
	fmt.Fprintln(os.Stderr, "       agentflow run --detach -f workflow.yaml [-C repo] [--codex-bin path] [--set key=value]")
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
	stdout := os.Stdout
	return runAllStatusTo(repoRoot, jsonOutput, stdout, statusOutputIsTTY(stdout), clioutput.ColorEnabled(stdout))
}

func runAllStatusTo(repoRoot string, jsonOutput bool, out io.Writer, tty, color bool) error {
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
		return clioutput.WriteJSONWithTTY(out, statusAllOutput{SchemaVersion: 1, Repo: repo.Root, Workflows: statuses}, tty)
	}

	presenter := clioutput.NewPresenterWithMode(out, tty, color)
	label := func(name string) string { return presenter.Style(clioutput.RoleLabel, name+":") }
	fmt.Fprintf(out, "%s %s\n%s %d\n", label("repository"), repo.Root, label("workflows"), len(statuses))
	for _, status := range statuses {
		fmt.Fprintf(out, "- %s %s\n  %s %s\n", label("workflow"), status.Workflow, label("state"), presenter.Style(clioutput.StateRole(status.State), status.State))
		if status.Namespace != "" {
			fmt.Fprintf(out, "  %s %s\n", label("namespace"), status.Namespace)
		}
		if status.Error != "" {
			fmt.Fprintf(out, "  %s %s\n", label("error"), presenter.Style(clioutput.RoleError, status.Error))
			continue
		}
		if status.Initialized {
			fmt.Fprintf(out, "  %s %s\n  %s %s\n  %s %s\n", label("base"), status.Base, label("branch"), status.Branch, label("head"), status.Head)
		}
		if status.ActivePhase != "" {
			fmt.Fprintf(out, "  %s %s\n", label("active_phase"), presenter.Style(clioutput.RoleAccent, status.ActivePhase))
		}
		completeRole := clioutput.RoleMuted
		if status.Complete {
			completeRole = clioutput.RoleSuccess
		}
		fmt.Fprintf(out, "  %s %s\n", label("complete"), presenter.Style(completeRole, fmt.Sprint(status.Complete)))
		if status.ProcessLiveness != "" {
			fmt.Fprintf(out, "  %s %s\n", label("process_liveness"), presenter.Style(clioutput.StateRole(status.ProcessLiveness), status.ProcessLiveness))
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
