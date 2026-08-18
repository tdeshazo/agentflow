// Package clioutput contains presentation helpers for command-line output.
package clioutput

import (
	"encoding/json"
	"io"
	"os"

	"github.com/mattn/go-isatty"
)

// IsTTY reports whether w is a terminal-backed output file. Unknown writers
// are intentionally treated as non-terminals so redirected output stays
// machine-friendly.
func IsTTY(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok || file == nil {
		return false
	}

	fd := file.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// WriteJSON writes value as compact JSON for non-terminal output and as
// indented JSON for terminal output. Both forms end with exactly one newline.
func WriteJSON(w io.Writer, value any) error {
	return WriteJSONWithTTY(w, value, IsTTY(w))
}

// WriteJSONWithTTY is the deterministic formatting seam used by tests and by
// callers that already know the output terminal state.
func WriteJSONWithTTY(w io.Writer, value any, tty bool) error {
	var (
		data []byte
		err  error
	)
	if tty {
		data, err = json.MarshalIndent(value, "", "  ")
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}

	data = append(data, '\n')
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
