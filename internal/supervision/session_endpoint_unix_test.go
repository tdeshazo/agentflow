//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervision

import (
	"errors"
	"strings"
	"testing"
)

func TestOverlongUnixEndpointIsNotReportedAsHostUnavailable(t *testing.T) {
	_, err := listenSession("/" + strings.Repeat("x", unixSocketPathMax))
	if err == nil {
		t.Fatal("overlong Unix endpoint was accepted")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("deterministic endpoint defect was reported as host unavailability: %v", err)
	}
}
