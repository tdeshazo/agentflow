package agentflowcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/tdeshazo/agentflow/internal/clioutput"
	"github.com/tdeshazo/agentflow/internal/engine"
	"github.com/tdeshazo/agentflow/internal/gitstate"
	"github.com/tdeshazo/agentflow/internal/observability"
	"github.com/tdeshazo/agentflow/internal/workflow"
	"github.com/tdeshazo/agentflow/provider"
	codexprovider "github.com/tdeshazo/agentflow/provider/codex"
)

type sets struct {
	values []string
	parsed map[string]string
}

func configuredSets(parameters map[string]string) sets {
	if len(parameters) == 0 {
		return sets{}
	}
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := sets{parsed: make(map[string]string, len(parameters))}
	for _, key := range keys {
		value := parameters[key]
		result.parsed[key] = value
		result.values = append(result.values, key+"="+value)
	}
	return result
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

const v1alpha1DeprecationWarning = "warning: agentflow.dev/v1alpha1 is deprecated for new authoring; " +
	"use agentflow.dev/v1alpha4 for new workflows and run 'agentflow migrate --check' to assess an existing workflow"

var statusOutputIsTTY = clioutput.IsTTY

var currentWorkingDirectory = os.Getwd
var workflowHomeDirectory = os.UserHomeDir

// Main runs the AgentFlow CLI and exits with a non-zero status on failure.
func Main() {
	if err := run(); err != nil {
		writeTopLevelError(os.Stderr, clioutput.NewPresenter(os.Stderr), err)
		os.Exit(1)
	}
}

func writeTopLevelError(out io.Writer, presenter clioutput.Presenter, err error) {
	fmt.Fprintln(out, presenter.Style(clioutput.RoleError, "agentflow:"), err)
}

func run() error {
	return runArgs(os.Args[1:])
}

func runArgs(args []string) error {
	return runArgsWithIO(args, os.Stdin, os.Stdout)
}

func runArgsWithIO(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	cmd := args[0]
	if cmd == "checkout" {
		// checkout is intentionally a complete compatibility alias for switch.
		cmd = "switch"
	}
	configRoot, err := discoveryRoot(commandLineRepo(args[1:]))
	if err != nil {
		return err
	}
	config, err := loadCLIConfig(configRoot, workflowHomeDirectory)
	if err != nil {
		return err
	}

	codexBinDefault := configuredString(config.CodexBin, "codex")
	detachDefault := cmd == "run" && configuredBool(config.Run.Detach, false)
	if os.Getenv(detachedChildEnv) == "1" {
		detachDefault = false
	}
	jsonDefault := cmd == "status" && configuredBool(config.Status.JSON, false)
	allDefault := cmd == "status" && configuredBool(config.Status.All, false)
	detailDefault := cmd == "status" && configuredBool(config.Status.Detail, false)
	tailDefault := -1
	followDefault := false
	if cmd == "logs" {
		tailDefault = configuredInt(config.Logs.Tail, -1)
		followDefault = configuredBool(config.Logs.Follow, false)
	}
	expandedDefault := cmd == "plan" && configuredBool(config.Plan.Expanded, false)

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	file := fs.String("f", "", "workflow YAML file")
	repo := fs.String("C", "", "repository root override")
	codexBin := fs.String("codex-bin", codexBinDefault, "Codex CLI binary")
	detach := fs.Bool("detach", detachDefault, "start the workflow in a detached child process (run only)")
	jsonOutput := fs.Bool("json", jsonDefault, "emit machine-readable JSON (status only)")
	all := fs.Bool("all", allDefault, "inspect every discovered workflow (status only)")
	detail := fs.Bool("detail", detailDefault, "include bounded recent trace events (status only)")
	workflowName := fs.String("workflow", "", "workflow name (logs only)")
	tail := fs.Int("tail", tailDefault, "show the final N log lines (logs only)")
	follow := fs.Bool("follow", followDefault, "follow appended workflow log output (logs only)")
	expanded := fs.Bool("expanded", expandedDefault, "show resolved executable plan")
	check := fs.Bool("check", false, "report v1alpha1 migration classifications without rewriting")
	clearSelection := fs.Bool("clear", false, "clear the active workflow selection (switch only)")
	overrides := configuredSets(config.Parameters)
	fs.Var(&overrides, "set", "parameter override (key=value), repeatable")
	flagArgs, positional := splitCommandArgs(args[1:])
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	explicit := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if cmd == "status" {
		switch {
		case explicit["detail"] && *detail && !(explicit["all"] && *all):
			*all = false
		case explicit["all"] && *all && !(explicit["detail"] && *detail):
			*detail = false
		}
	}
	configuredWorkflow := configWorkflow(config, cmd)
	if len(positional) > 0 || *file != "" {
		if cmd == "status" && !explicit["all"] {
			*all = false
		}
		configuredWorkflow = ""
	} else if cmd == "status" && explicit["all"] && *all {
		configuredWorkflow = ""
	}
	if cmd == "logs" {
		if explicit["tail"] {
			*follow = false
		} else if explicit["follow"] && *follow {
			*tail = -1
		}
	}
	if *file != "" && len(positional) > 0 {
		return fmt.Errorf("-f and a positional workflow selector are mutually exclusive")
	}
	if len(positional) > 1 {
		return fmt.Errorf("expected at most one positional workflow selector, got %d", len(positional))
	}
	if len(positional) > 0 {
		if cmd == "status" && *all {
			return fmt.Errorf("status --all does not accept a positional workflow selector")
		}
		switch cmd {
		case "run", "status", "reset", "validate", "plan", "migrate", "switch":
			if cmd == "switch" && positional[0] == "-" {
				break
			}
			if err := workflow.ValidateSelector(positional[0]); err != nil {
				return err
			}
		case "logs":
			return fmt.Errorf("logs does not accept a positional workflow selector; use --workflow")
		case "current", "workflows":
			return fmt.Errorf("%s does not accept a positional workflow selector", cmd)
		default:
			return fmt.Errorf("%s does not accept a positional workflow selector", cmd)
		}
	}
	tailProvided := cmd == "logs" && (config.Logs.Tail != nil || explicit["tail"])
	if explicit["follow"] && *follow {
		tailProvided = false
	}
	if *jsonOutput && cmd != "status" {
		return fmt.Errorf("--json is only supported with status")
	}
	if *all && cmd != "status" {
		return fmt.Errorf("--all is only supported with status")
	}
	if *detail && cmd != "status" {
		return fmt.Errorf("--detail is only supported with status")
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
	if *clearSelection && cmd != "switch" {
		return fmt.Errorf("--clear is only supported with switch")
	}
	if *check && cmd != "migrate" {
		return fmt.Errorf("--check is only supported with migrate")
	}
	if cmd == "migrate" && !*check {
		return fmt.Errorf("migrate requires --check; automatic rewriting is not implemented")
	}
	if cmd == "status" && *all && *file != "" {
		return fmt.Errorf("status selectors --all and -f are mutually exclusive")
	}
	if cmd == "status" && *all && *detail {
		return fmt.Errorf("status selectors --all and --detail are mutually exclusive")
	}
	if cmd == "switch" {
		for _, name := range []string{"f", "codex-bin", "detach", "json", "all", "detail", "workflow", "tail", "follow", "expanded", "check", "set"} {
			if explicit[name] {
				return fmt.Errorf("--%s is not supported with switch", name)
			}
		}
		if *clearSelection && len(positional) > 0 {
			return fmt.Errorf("switch --clear does not accept a workflow selector")
		}
		return runWorkflowSwitchWithIO(*repo, positional, *clearSelection, in, out)
	}
	if cmd == "current" || cmd == "workflows" {
		for _, name := range []string{"f", "codex-bin", "detach", "json", "all", "detail", "workflow", "tail", "follow", "expanded", "check", "clear", "set"} {
			if explicit[name] {
				return fmt.Errorf("--%s is not supported with %s", name, cmd)
			}
		}
		if cmd == "current" {
			return runCurrentWorkflow(*repo, out)
		}
		return runWorkflows(*repo, out)
	}
	if cmd == "status" && *all {
		return runAllStatus(*repo, *jsonOutput)
	}

	workflowFile := *file
	repositoryScoped := cmd == "run" || cmd == "status" || cmd == "reset"
	var result workflow.Result
	workflowValidated := false
	if workflowFile != "" {
		workflowFile, err = filepath.Abs(workflowFile)
		if err != nil {
			return fmt.Errorf("resolve workflow file: %w", err)
		}
		if _, statErr := os.Stat(workflowFile); repositoryScoped && statErr == nil {
			// An explicit source file can be validated without repository state.
			// Give invalid/unsupported source diagnostics precedence so no workspace
			// lookup or mutation can obscure a pre-execution contract failure.
			result = workflow.ValidateFile(workflowFile)
			workflowValidated = true
			if result.Status == workflow.Invalid {
				return diagnosticsError(result)
			}
			if result.Status == workflow.Unsupported {
				return fmt.Errorf("workflow is valid but unsupported by this runtime: %s", diagnosticsError(result))
			}
		}
	}
	repoRoot := *repo
	if repositoryScoped {
		repository, err := targetRepo(*repo)
		if err != nil {
			return err
		}
		repoRoot = repository.Root
	} else if len(positional) > 0 {
		repoRoot, err = discoveryRoot(*repo)
		if err != nil {
			return err
		}
	} else if workflowFile == "" && usesActiveWorkflowSelection(cmd) {
		repoRoot, err = discoveryRoot(*repo)
		if err != nil {
			return err
		}
	}
	selector := configuredWorkflow
	if len(positional) == 1 {
		selector = positional[0]
	}
	if cmd == "logs" && explicit["workflow"] {
		selector = *workflowName
	}
	selectedFromState := false
	if selector == "" && workflowFile == "" && usesActiveWorkflowSelection(cmd) {
		// Active selection is a local convenience default. It is intentionally
		// consulted only after explicit and configured selectors, and it does
		// not create or inspect durable workflow execution state.
		selectionRepo, selectionErr := targetRepo(repoRoot)
		if selectionErr == nil {
			selection, found, readErr := newSelectionStore(selectionRepo).Read()
			if readErr != nil {
				return readErr
			}
			if found {
				selector = selection.Current
				selectedFromState = true
			}
		}
	}
	logWorkflowName := selector
	if selectedFromState {
		activeWorkflowFile, err := workflow.ResolveFile(repoRoot, selector, workflowHomeDirectory)
		if err != nil {
			return staleActiveWorkflowSelectionError(selector, err)
		}
		if cmd == "logs" {
			document, decodeErr := workflow.Decode(activeWorkflowFile)
			if decodeErr != nil {
				return fmt.Errorf("active workflow selection %q cannot load workflow: %w", selector, decodeErr)
			}
			if document == nil || document.Workflow == nil || document.Workflow.Metadata.Name == "" {
				return fmt.Errorf("active workflow selection %q has no runtime workflow name", selector)
			}
			logWorkflowName = document.Workflow.Metadata.Name
		}
	}
	if cmd == "logs" {
		if *file != "" {
			return fmt.Errorf("logs does not accept -f; use --workflow")
		}
		if selector == "" {
			return fmt.Errorf("logs requires --workflow")
		}
		return runLogs(repoRoot, logWorkflowName, *tail, *follow)
	}
	if selector != "" {
		workflowFile, err = workflow.ResolveFile(repoRoot, selector, workflowHomeDirectory)
		if err != nil {
			if selectedFromState {
				return staleActiveWorkflowSelectionError(selector, err)
			}
			return err
		}
	}
	if workflowFile == "" {
		if requiresWorkflowSelector(cmd) && workflowPickerInteractive(in, out) {
			workflowFile, err = pickWorkflow(repoRoot, in, out, workflowHomeDirectory)
			if err != nil {
				return err
			}
		} else if requiresWorkflowSelector(cmd) {
			return missingWorkflowSelectorError(cmd)
		} else {
			return fmt.Errorf("-f workflow YAML is required")
		}
	}
	if !workflowValidated {
		result = workflow.ValidateFile(workflowFile)
	}
	if cmd == "validate" {
		return writeValidationResult(clioutput.NewPresenter(out), result)
	}
	if cmd == "migrate" {
		report, checkErr := workflow.MigrationCheckFile(workflowFile)
		if checkErr != nil {
			return checkErr
		}
		encoded, marshalErr := yaml.Marshal(report)
		if marshalErr != nil {
			return marshalErr
		}
		return clioutput.NewPresenterWithPresentation(out, clioutput.PresentationRaw).RawBytes(encoded)
	}
	if result.Status == workflow.Invalid {
		return diagnosticsError(result)
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
		encoded, err := yaml.Marshal(plan)
		if err != nil {
			return err
		}
		return clioutput.NewPresenterWithPresentation(out, clioutput.PresentationRaw).RawBytes(encoded)
	}
	if result.Status == workflow.Unsupported {
		return fmt.Errorf("workflow is valid but unsupported by this runtime: %s", diagnosticsError(result))
	}
	if *detach {
		pid, err := launchDetachedRun(os.Args[0], workflowFile, repoRoot, *codexBin, overrides.Values(), result.Document.Workflow.Metadata.Name)
		if err != nil {
			return err
		}
		clioutput.NewPresenter(os.Stdout).Line(clioutput.RoleSuccess, "detached workflow %q started (pid %d)", result.Document.Workflow.Metadata.Name, pid)
		return nil
	}
	w := result.Document.Workflow
	providers := map[string]provider.Provider{"codex": codexprovider.Provider{Binary: *codexBin}}
	stateOnly := cmd == "status" || cmd == "reset"
	e, err := engine.New(w, providers, engine.Options{RepoRoot: repoRoot, Overrides: overrides.Map(), StateOnly: stateOnly, Detached: os.Getenv(detachedChildEnv) == "1" && cmd == "run"})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch cmd {
	case "run":
		if err := e.Run(ctx); err != nil {
			return appendRecoveryGuidance(err, e.FailureRecoveryGuidance())
		}
		return nil
	case "status":
		if *jsonOutput {
			stdout := os.Stdout
			if *detail {
				return e.StatusJSONWithDetailTo(stdout, statusOutputIsTTY(stdout))
			}
			return e.StatusJSONTo(stdout, statusOutputIsTTY(stdout))
		}
		if *detail {
			return e.StatusWithDetail()
		}
		return e.Status()
	case "reset":
		return e.Reset()
	default:
		return usage()
	}
}

func appendRecoveryGuidance(err error, guidance string) error {
	if guidance == "" {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, guidance)
}

func requiresWorkflowSelector(cmd string) bool {
	switch cmd {
	case "run", "status", "reset", "validate", "plan", "migrate":
		return true
	default:
		return false
	}
}

func usesActiveWorkflowSelection(cmd string) bool {
	if requiresWorkflowSelector(cmd) {
		return true
	}
	return cmd == "logs"
}

func staleActiveWorkflowSelectionError(selector string, err error) error {
	return fmt.Errorf(
		"active workflow selection %q is stale: %w; "+
			"run 'agentflow switch <workflow-name>' to select a discovered workflow or "+
			"'agentflow switch --clear' to clear it",
		selector,
		err,
	)
}

func usage() error {
	writeUsage(os.Stderr, clioutput.NewPresenter(os.Stderr))
	return fmt.Errorf("invalid command")
}

func writeUsage(out io.Writer, presenter clioutput.Presenter) {
	fmt.Fprintf(out, "%s agentflow <validate|plan|run|status|reset|migrate --check> [-f workflow.yaml | workflow-name] [-C repo] [--expanded] [--json] [--detail] [--set key=value]\n", presenter.Label("usage"))
	fmt.Fprintln(out, "       agentflow run --detach [-f workflow.yaml | workflow-name] [-C repo] [--codex-bin path] [--set key=value]")
	fmt.Fprintln(out, "       agentflow switch [workflow-name|-] [-C repo] | agentflow switch --clear [-C repo]")
	fmt.Fprintln(out, "       agentflow checkout ...  # compatibility alias for switch")
	fmt.Fprintln(out, "       agentflow current [-C repo]")
	fmt.Fprintln(out, "       agentflow workflows [-C repo]")
	fmt.Fprintln(out, "       omit the workflow selector in a terminal to choose a discovered workflow interactively")
	fmt.Fprintln(out, "       agentflow status --all [-C repo] [--json]")
	fmt.Fprintln(out, "       agentflow status [-f workflow.yaml | workflow-name] [-C repo] --detail [--json]")
	fmt.Fprintln(out, "       agentflow logs [--workflow name] [-C repo] [--tail n|--follow]")
	fmt.Fprintln(out, "       defaults load from <repo>/.agentflow/config.toml and ~/.agentflow/config.toml")
}

func runWorkflowSwitch(repoRoot string, positional []string, clear bool, out io.Writer) error {
	return runWorkflowSwitchWithIO(repoRoot, positional, clear, os.Stdin, out)
}

func runWorkflowSwitchWithIO(repoRoot string, positional []string, clear bool, in io.Reader, out io.Writer) error {
	repo, err := targetRepo(repoRoot)
	if err != nil {
		return err
	}
	store := newSelectionStore(repo)
	if clear {
		if err := store.Clear(); err != nil {
			return err
		}
		fmt.Fprintln(out, "cleared active workflow selection")
		return nil
	}
	if len(positional) == 0 {
		if !workflowPickerInteractive(in, out) {
			return missingWorkflowSwitchSelectorError()
		}
		selector, err := pickWorkflowSelector(repo.Root, in, out, workflowHomeDirectory)
		if err != nil {
			return err
		}
		if err := store.Select(selector); err != nil {
			return err
		}
		fmt.Fprintln(out, selector)
		return nil
	}

	selector := positional[0]
	if selector == "-" {
		selection, found, err := store.Read()
		if err != nil {
			return err
		}
		if !found || selection.Previous == "" {
			return fmt.Errorf("no previous workflow selection")
		}
		if _, err := workflow.ResolveFile(repo.Root, selection.Previous, workflowHomeDirectory); err != nil {
			return fmt.Errorf("previous workflow selection %q is stale: %w", selection.Previous, err)
		}
		selector, err = store.SwitchPrevious()
		if err != nil {
			return err
		}
		fmt.Fprintln(out, selector)
		return nil
	}

	if _, err := workflow.ResolveFile(repo.Root, selector, workflowHomeDirectory); err != nil {
		return err
	}
	if err := store.Select(selector); err != nil {
		return err
	}
	fmt.Fprintln(out, selector)
	return nil
}

// runCurrentWorkflow emits only the stored logical selector. It deliberately
// does not resolve discovery: it remains useful for shell scripts to identify
// and clear a stale selection.
func runCurrentWorkflow(repoRoot string, out io.Writer) error {
	repo, err := targetRepo(repoRoot)
	if err != nil {
		return err
	}
	selection, found, err := newSelectionStore(repo).Read()
	if err != nil {
		return err
	}
	if found {
		fmt.Fprintln(out, selection.Current)
	}
	return nil
}

func runWorkflows(repoRoot string, out io.Writer) error {
	repo, err := targetRepo(repoRoot)
	if err != nil {
		return err
	}
	discovery, err := workflow.DiscoverFiles(repo.Root, workflowHomeDirectory)
	if err != nil {
		return err
	}
	selection, found, err := newSelectionStore(repo).Read()
	if err != nil {
		return err
	}
	if found {
		if _, err := workflow.ResolveFile(repo.Root, selection.Current, workflowHomeDirectory); err != nil {
			return staleActiveWorkflowSelectionError(selection.Current, err)
		}
	}
	for _, file := range discovery.Files {
		marker := " "
		if found && file.Name == selection.Current {
			marker = "*"
		}
		fmt.Fprintf(out, "%s %s\n", marker, file.Name)
	}
	return nil
}

func writeValidationResult(presenter clioutput.Presenter, result workflow.Result) error {
	isDeprecatedV1Alpha1 := result.Status != workflow.Invalid &&
		result.Document != nil &&
		result.Document.Workflow != nil &&
		result.Document.Workflow.APIVersion == "agentflow.dev/v1alpha1"
	if isDeprecatedV1Alpha1 {
		presenter.Line(clioutput.RoleWarning, "%s", v1alpha1DeprecationWarning)
	}
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
	presenter.Metadata("repository", repo.Root)
	presenter.Metadata("workflows", fmt.Sprint(len(statuses)))
	for _, status := range statuses {
		presenter.ListItem("workflow", status.Workflow)
		presenter.IndentedMetadata("  ", "state", status.State, clioutput.StateRole(status.State))
		if status.Namespace != "" {
			presenter.IndentedMetadata("  ", "namespace", status.Namespace, clioutput.RolePlain)
		}
		if status.Error != "" {
			presenter.IndentedMetadata("  ", "error", status.Error, clioutput.RoleError)
			continue
		}
		if status.Initialized {
			presenter.IndentedMetadata("  ", "base", status.Base, clioutput.RolePlain)
			presenter.IndentedMetadata("  ", "branch", status.Branch, clioutput.RolePlain)
			presenter.IndentedMetadata("  ", "head", status.Head, clioutput.RolePlain)
		}
		if status.ActivePhase != "" {
			presenter.IndentedMetadata("  ", "active_phase", status.ActivePhase, clioutput.RoleAccent)
		}
		if status.FailureStage != "" {
			presenter.IndentedMetadata("  ", "failure_stage", status.FailureStage, clioutput.RoleWarning)
			presenter.IndentedMetadata("  ", "last_error", status.LastError, clioutput.RoleError)
		}
		if status.QuarantinePath != "" {
			presenter.IndentedMetadata("  ", "quarantine", status.QuarantinePath, clioutput.RoleWarning)
		}
		if status.Recovery != "" {
			presenter.IndentedMetadata("  ", "recovery", status.Recovery, clioutput.StateRole(status.Recovery))
			presenter.IndentedMetadata("  ", "next_action", status.NextAction, clioutput.StateRole(status.NextAction))
		}
		writeStatusIntegrityViolation(presenter, status.IntegrityViolation)
		completeRole := clioutput.RoleMuted
		if status.Complete {
			completeRole = clioutput.RoleSuccess
		}
		presenter.IndentedMetadata("  ", "complete", fmt.Sprint(status.Complete), completeRole)
		if status.ProcessLiveness != "" {
			presenter.IndentedMetadata(
				"  ",
				"process_liveness",
				status.ProcessLiveness,
				clioutput.StateRole(status.ProcessLiveness),
			)
		}
	}
	return nil
}

func writeStatusIntegrityViolation(presenter clioutput.Presenter, violation *gitstate.IntegrityViolation) {
	if violation == nil {
		return
	}
	presenter.IndentedMetadata("  ", "integrity_rule", violation.IntegrityRule, clioutput.RoleError)
	for _, item := range []struct {
		label string
		paths []string
	}{
		{label: "changed", paths: violation.Changed},
		{label: "added", paths: violation.Added},
		{label: "removed", paths: violation.Removed},
	} {
		if len(item.paths) == 0 {
			presenter.IndentedMetadata("  ", item.label, "[]", clioutput.RolePlain)
			continue
		}
		presenter.Line(clioutput.RolePlain, "  %s:", item.label)
		for _, path := range item.paths {
			presenter.Line(clioutput.RolePlain, "    - %s", path)
		}
	}
}

func targetRepo(root string) (gitstate.Repo, error) {
	if root == "" {
		var err error
		root, err = currentWorkingDirectory()
		if err != nil {
			return gitstate.Repo{}, fmt.Errorf("resolve current working directory: %w", err)
		}
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

func discoveryRoot(root string) (string, error) {
	if root == "" {
		var err error
		root, err = currentWorkingDirectory()
		if err != nil {
			return "", fmt.Errorf("resolve current working directory: %w", err)
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workflow discovery root: %w", err)
	}
	return abs, nil
}

func splitCommandArgs(args []string) (flagArgs, positional []string) {
	valueFlags := map[string]bool{
		"-f":          true,
		"-C":          true,
		"--C":         true,
		"--codex-bin": true,
		"-codex-bin":  true,
		"--workflow":  true,
		"-workflow":   true,
		"--tail":      true,
		"-tail":       true,
		"--set":       true,
		"-set":        true,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		flagArgs = append(flagArgs, arg)
		name := strings.SplitN(arg, "=", 2)[0]
		if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, positional
}

func commandLineRepo(args []string) string {
	var root string
	valueFlags := map[string]bool{
		"-f":          true,
		"--codex-bin": true,
		"-codex-bin":  true,
		"--workflow":  true,
		"-workflow":   true,
		"--tail":      true,
		"-tail":       true,
		"--set":       true,
		"-set":        true,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			return root
		case arg == "-C" || arg == "--C":
			if i+1 < len(args) {
				i++
				root = args[i]
			}
		case strings.HasPrefix(arg, "-C="):
			root = strings.TrimPrefix(arg, "-C=")
		case strings.HasPrefix(arg, "--C="):
			root = strings.TrimPrefix(arg, "--C=")
		default:
			name := strings.SplitN(arg, "=", 2)[0]
			if valueFlags[name] && !strings.Contains(arg, "=") && i+1 < len(args) {
				i++
			}
		}
	}
	return root
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
		raw := clioutput.NewPresenterWithPresentation(os.Stdout, clioutput.PresentationRaw)
		return observability.Follow(ctx, path, raw.Out)
	}
	if tail >= 0 {
		data, err = observability.Tail(data, tail)
		if err != nil {
			return err
		}
	}
	return clioutput.NewPresenterWithPresentation(os.Stdout, clioutput.PresentationRaw).RawBytes(data)
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
