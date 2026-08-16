// Command jq exposes the repository-managed gojq tool under the name used by
// the dogfood validation command: go tool jq.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

func main() {
	command := exec.Command("go", append([]string{"tool", "gojq"}, os.Args[1:]...)...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			os.Exit(exitError.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "run gojq: %v\n", err)
		os.Exit(1)
	}
}
