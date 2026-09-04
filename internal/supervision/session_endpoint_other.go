//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package supervision

import (
	"fmt"
	"net"
)

func sessionEndpointPath(string) (string, error) {
	return "", fmt.Errorf("%w: local session endpoints are unsupported on this platform", ErrUnavailable)
}

func listenSession(string) (net.Listener, error) {
	return nil, fmt.Errorf("%w: local session endpoints are unsupported on this platform", ErrUnavailable)
}
