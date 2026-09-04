//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !solaris

package observability

import (
	"errors"
	"os"
)

var errPrivateLogUnsupported = errors.New("owner-private no-follow workflow logs are unavailable on this platform")

func openPrivateAppend(string) (*os.File, error) { return nil, errPrivateLogUnsupported }
func openPrivateRead(string) (*os.File, error)   { return nil, errPrivateLogUnsupported }
func validatePrivateDirectory(string) error      { return errPrivateLogUnsupported }
func validatePrivateFile(*os.File) error         { return errPrivateLogUnsupported }
