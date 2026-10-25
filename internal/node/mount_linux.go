// SPDX-License-Identifier: GPL-3.0-or-later

package node

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Mounted reports whether dst is a mount point in the container's mount
// namespace, read from the host side (no namespace switch, no hang risk).
func Mounted(pid int, dst string) bool {
	f, err := os.Open("/proc/" + strconv.Itoa(pid) + "/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 4 && fields[4] == dst {
			return true
		}
	}
	return false
}

// BindReadOnly attaches src (host path) at dst inside the container's mount
// namespace, read-only. open_tree(OPEN_TREE_CLONE) from the host namespace
// yields a detached mount no namespace check applies to; move_mount then
// lands it after setns. Kernel 5.12+.
func BindReadOnly(pid int, src, dst string) error {
	if pid <= 0 {
		return fmt.Errorf("container not running")
	}
	if Mounted(pid, dst) {
		return nil
	}
	errc := make(chan error, 1)
	go func() {
		// the thread switches namespace for good: lock it and never unlock,
		// so the runtime discards it when this goroutine ends
		runtime.LockOSThread()
		errc <- func() error {
			tree, err := unix.OpenTree(unix.AT_FDCWD, src, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC)
			if err != nil {
				return fmt.Errorf("open_tree: %w", err)
			}
			defer unix.Close(tree)
			// Mark the detached tree read-only before exposing it to the server.
			// Requires Linux 5.12+; avoids a writable window after move_mount.
			if err := unix.MountSetattr(tree, "", unix.AT_EMPTY_PATH, &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY}); err != nil {
				return fmt.Errorf("read-only mount attributes (kernel 5.12+ required): %w", err)
			}
			ns, err := unix.Open("/proc/"+strconv.Itoa(pid)+"/ns/mnt", unix.O_RDONLY|unix.O_CLOEXEC, 0)
			if err != nil {
				return fmt.Errorf("open mnt ns: %w", err)
			}
			defer unix.Close(ns)
			containerRoot, err := unix.Open("/proc/"+strconv.Itoa(pid)+"/root", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				return fmt.Errorf("open container root: %w", err)
			}
			defer unix.Close(containerRoot)
			// setns(CLONE_NEWNS) refuses a thread that shares fs state with
			// its siblings, which every Go thread does: unshare it first
			if err := unix.Unshare(unix.CLONE_FS); err != nil {
				return fmt.Errorf("unshare fs: %w", err)
			}
			if err := unix.Setns(ns, unix.CLONE_NEWNS); err != nil {
				return fmt.Errorf("setns: %w", err)
			}
			// setns does not change root/cwd. Restrict this discarded thread's
			// filesystem view to the pinned container root before using dst.
			if err := unix.Fchdir(containerRoot); err != nil {
				return err
			}
			if err := unix.Chroot("."); err != nil {
				return err
			}
			if err := unix.Chdir("/"); err != nil {
				return err
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("mkdir in container: %w", err)
			}
			if err := unix.MoveMount(tree, "", unix.AT_FDCWD, dst, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
				return fmt.Errorf("move_mount: %w", err)
			}
			if err := unix.Mount("none", dst, "none", unix.MS_REMOUNT|unix.MS_BIND|unix.MS_RDONLY, ""); err != nil {
				return fmt.Errorf("remount ro: %w", err)
			}
			return nil
		}()
	}()
	return <-errc
}
