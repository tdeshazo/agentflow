package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tdeshazo/agentflow/internal/workflow"
)

func main() {
	out := flag.String("out", "schema", "directory for generated workflow schemas")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fatal(err)
	}
	for _, version := range []string{"agentflow.dev/v1alpha1", "agentflow.dev/v1alpha2", "agentflow.dev/v1alpha3", "agentflow.dev/v1alpha4"} {
		contents, err := workflow.GeneratedSchema(version)
		if err != nil {
			fatal(err)
		}
		name := filepath.Join(*out, version[len("agentflow.dev/"):]+".schema.json")
		if err := os.WriteFile(name, append(contents, '\n'), 0o644); err != nil {
			fatal(err)
		}
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
