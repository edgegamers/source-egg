package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Nonblocking open prevents a replaced FIFO from blocking before f.Stat.
func openReceipt(root *os.Root, name string) (*os.File, error) {
	if !filepath.IsLocal(name) {
		return nil, errors.New("non-local JSON receipt")
	}
	base, e := root.Open(".")
	if e != nil {
		return nil, e
	}
	defer base.Close()
	// os.Root may resolve symlinks itself after O_NOFOLLOW yields ELOOP. Walk
	// pinned directory descriptors directly so every component rejects links.
	fd := int(base.Fd())
	owned := -1
	defer func() {
		if owned >= 0 {
			unix.Close(owned)
		}
	}()
	parts := strings.Split(filepath.Clean(name), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, &os.PathError{Op: "open", Path: name, Err: e}
		}
		if owned >= 0 {
			unix.Close(owned)
		}
		fd = next
		owned = next
	}
	final, e := unix.Openat(fd, parts[len(parts)-1], unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: e}
	}
	return os.NewFile(uintptr(final), name), nil
}
