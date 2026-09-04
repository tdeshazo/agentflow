//go:build !linux

package supervision

import (
	"errors"
	"testing"
)

func TestStartMapsUnavailableStableIdentityToErrUnavailable(t *testing.T) {
	repo := newSessionRepo(t)
	_, err := Start(repo, "unsupported", "run_6123456789abcdef0123456789abcdef", func() {})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Start error = %v, want ErrUnavailable", err)
	}
}
