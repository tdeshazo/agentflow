package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/tdeshazo/agentflow-spec/internal/engine"
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
	expanded := fs.Bool("expanded", false, "show resolved executable plan")
	var overrides sets
	fs.Var(&overrides, "set", "parameter override (key=value), repeatable")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("-f workflow YAML is required")
	}
	if *jsonOutput && cmd != "status" {
		return fmt.Errorf("--json is only supported with status")
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
	return fmt.Errorf("invalid command")
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
