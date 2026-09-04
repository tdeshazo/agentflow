//go:build linux

package observability

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestValidatePrivateDirectoryFDRejectsUnsafeOpenedDirectory(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(directory, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := validatePrivateDirectoryFD(fd); err == nil {
		t.Fatal("opened non-private directory descriptor was accepted")
	}
}

func TestOpenPrivateLogRejectsReplacedDirectorySymlink(t *testing.T) {
	parent := t.TempDir()
	original := filepath.Join(parent, "logs")
	replacement := filepath.Join(parent, "replacement")
	if err := os.Mkdir(original, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacement, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(replacement, original); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateAppend(filepath.Join(original, "events.jsonl")); err == nil {
		t.Fatal("replacement directory symlink was accepted")
	}
}
