package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
	if len(os.Args) < 2 {
		return usage()
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	file := fs.String("f", "", "workflow YAML file")
	repo := fs.String("C", "", "repository root override")
	codexBin := fs.String("codex-bin", "codex", "Codex CLI binary")
	var overrides sets
	fs.Var(&overrides, "set", "parameter override (key=value), repeatable")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *file == "" {
		return fmt.Errorf("-f workflow YAML is required")
	}
	w, err := workflow.Load(*file)
	if err != nil {
		return err
	}
	providers := map[string]provider.Provider{"codex": codexprovider.Provider{Binary: *codexBin}}
	e, err := engine.New(w, providers, engine.Options{RepoRoot: *repo, Overrides: map[string]string(overrides)})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch cmd {
	case "run":
		return e.Run(ctx)
	case "status":
		return e.Status()
	case "reset":
		return e.Reset()
	default:
		return usage()
	}
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: agentflow <run|status|reset> -f workflow.yaml [-C repo] [--set key=value]")
	return fmt.Errorf("invalid command")
}
