//go:build !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd && !solaris

package observability

import (
	"errors"
	"testing"
)

func TestPrivateLogOpenFailsClosedWithoutSafePrimitives(t *testing.T) {
	if _, err := openPrivateAppend("unsafe"); !errors.Is(err, errPrivateLogUnsupported) {
		t.Fatalf("openPrivateAppend error = %v", err)
	}
	if _, err := openPrivateRead("unsafe"); !errors.Is(err, errPrivateLogUnsupported) {
		t.Fatalf("openPrivateRead error = %v", err)
	}
}
