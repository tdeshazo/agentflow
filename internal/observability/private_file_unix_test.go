//go:build darwin || dragonfly || freebsd || netbsd || openbsd || solaris

package observability

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestValidatePrivateDirectoryFDRejectsUnsafeOpenedDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err := validatePrivateDirectoryFD(fd); err == nil {
		t.Fatal("opened non-private directory descriptor was accepted")
	}
}
