package content

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/edgegamers/source-egg/internal/fsx"
	"golang.org/x/sys/unix"
)

type fileOwner struct{ uid, gid int }

type fileState struct {
	Size    int64       `json:"size"`
	Mode    fs.FileMode `json:"mode"`
	ModTime int64       `json:"mtime"`
	Device  uint64      `json:"dev"`
	Inode   uint64      `json:"ino"`
}

type copiedFile struct {
	Source      fileState `json:"source"`
	Destination fileState `json:"destination"`
}

func copyState(info fs.FileInfo) fileState {
	stat := info.Sys().(*syscall.Stat_t)
	// ctime also changes on ownership-only checks performed by Wings before
	// every boot. It cannot be used to decide whether file data needs copying.
	return fileState{
		Size: info.Size(), Mode: info.Mode(), ModTime: info.ModTime().UnixNano(),
		Device: uint64(stat.Dev), Inode: stat.Ino,
	}
}

func ownership(info fs.FileInfo) (*fileOwner, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("file ownership unavailable")
	}
	return &fileOwner{uid: int(stat.Uid), gid: int(stat.Gid)}, nil
}

func linkDeployment(root *os.Root, source, name string) error {
	// Pin the destination directory under os.Root before linkat, so a server
	// cannot swap a parent symlink and redirect a privileged hardlink outside.
	dir, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer dir.Close()
	tmp := filepath.Base(name) + ".link-" + fsx.Token()
	if err := unix.Linkat(unix.AT_FDCWD, source, int(dir.Fd()), tmp, 0); err != nil {
		return fmt.Errorf("hardlink %s: %w (shared store and volume must support same-filesystem hardlinks; choose copy or symlink)", name, err)
	}
	defer unix.Unlinkat(int(dir.Fd()), tmp, 0)
	// Rename within the pinned directory; do not resolve the server-controlled
	// parent again after creating the link.
	return unix.Renameat(int(dir.Fd()), tmp, int(dir.Fd()), filepath.Base(name))
}
