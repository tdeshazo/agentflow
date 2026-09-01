//go:build linux

package gitstate

import (
	"io/fs"
	"testing"
)

func TestProcessStartTokenClassifiesReadErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want processInspection
	}{
		{name: "process absent", err: fs.ErrNotExist, want: processInspectionExited},
		{name: "process metadata inaccessible", err: fs.ErrPermission, want: processInspectionUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readFile := func(string) ([]byte, error) {
				return nil, tt.err
			}
			if _, got := processStartTokenWithRead(123, readFile); got != tt.want {
				t.Fatalf("process inspection = %v, want %v", got, tt.want)
			}
		})
	}
}
