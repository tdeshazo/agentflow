//go:build darwin || dragonfly || freebsd || netbsd || openbsd || solaris

package observability

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openPrivateAppend(path string) (*os.File, error) {
	return openPrivateUnix(path, unix.O_CREAT|unix.O_APPEND|unix.O_WRONLY, 0o600)
}

func openPrivateRead(path string) (*os.File, error) {
	return openPrivateUnix(path, unix.O_RDONLY, 0)
}

func openPrivateUnix(path string, flags int, mode uint32) (*os.File, error) {
	dirFD, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open private log directory: %w", err)
	}
	defer unix.Close(dirFD)
	if err := validatePrivateDirectoryFD(dirFD); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(dirFD, filepath.Base(path), flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, mode)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func validatePrivateDirectoryFD(fd int) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect opened private log directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("opened workflow log directory is not owner-private")
	}
	return nil
}

func validatePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || !privateOwner(info) {
		return fmt.Errorf("workflow log directory is not owner-private")
	}
	return nil
}

func validatePrivateFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || !privateOwner(info) {
		return fmt.Errorf("workflow log is not an owner-private regular file")
	}
	return nil
}

func privateOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*unix.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
