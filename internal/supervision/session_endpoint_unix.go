//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervision

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func sessionEndpointPath(key string) (string, error) {
	// /tmp keeps the sockaddr independent of caller-controlled TMPDIR length;
	// the UID-specific directory is validated and restricted by Start.
	path := filepath.Join("/tmp", fmt.Sprintf("af-%d", os.Geteuid()), key+".sock")
	if len([]byte(path)) > unixSocketPathMax {
		return "", fmt.Errorf("supervised session endpoint path is %d bytes; platform limit is %d", len([]byte(path)), unixSocketPathMax)
	}
	return path, nil
}

func listenSession(path string) (net.Listener, error) {
	if len([]byte(path)) > unixSocketPathMax {
		return nil, fmt.Errorf("supervised session endpoint path is %d bytes; platform limit is %d", len([]byte(path)), unixSocketPathMax)
	}
	listener, err := net.Listen("unix", path)
	if err == nil {
		return listener, nil
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EAFNOSUPPORT) || errors.Is(err, syscall.EPROTONOSUPPORT) ||
		errors.Is(err, syscall.ENOSYS) {
		return nil, fmt.Errorf("%w: listen on supervised session endpoint: %v", ErrUnavailable, err)
	}
	return nil, fmt.Errorf("listen on supervised session endpoint: %w", err)
}
