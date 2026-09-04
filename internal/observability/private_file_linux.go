//go:build linux

package observability

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func openPrivateAppend(path string) (*os.File, error) {
	return openPrivateLinux(path, syscall.O_CREAT|syscall.O_APPEND|syscall.O_WRONLY, 0o600)
}

func openPrivateRead(path string) (*os.File, error) {
	return openPrivateLinux(path, syscall.O_RDONLY, 0)
}

func openPrivateLinux(path string, flags int, mode uint32) (*os.File, error) {
	directory := filepath.Dir(path)
	dirFD, err := syscall.Open(directory, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open private log directory: %w", err)
	}
	defer syscall.Close(dirFD)
	if err := validatePrivateDirectoryFD(dirFD); err != nil {
		return nil, err
	}
	fd, err := syscall.Openat(dirFD, filepath.Base(path), flags|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func validatePrivateDirectoryFD(fd int) error {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect opened private log directory: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFDIR || stat.Mode&0o077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("opened workflow log directory is not owner-private")
	}
	return nil
}

func privateOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
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
